package cfgx

import (
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type nestedChild struct {
	Port int `yaml:"port" json:"port"`
}

func (c *nestedChild) Validate(fix bool) []error {
	if c.Port <= 0 {
		if fix {
			c.Port = 8080
		}
		return []error{errors.New("must be > 0")}
	}
	return nil
}

type nestedRoot struct {
	Server nestedChild `yaml:"server" json:"server"`
}

type orderedChild struct {
	Port  int `yaml:"port"`
	order *[]string
}

func (c *orderedChild) Validate(fix bool) []error {
	if c.order != nil {
		*c.order = append(*c.order, "child")
	}
	if c.Port <= 0 {
		if fix {
			c.Port = 8080
		}
		return []error{errors.New("must be > 0")}
	}
	return nil
}

type orderedRoot struct {
	Server orderedChild `yaml:"server"`
	order  *[]string
}

func (r *orderedRoot) Validate(fix bool) []error {
	if r.order != nil {
		*r.order = append(*r.order, "root")
	}
	return nil
}

type inlineWrap struct {
	nestedChild `yaml:",inline" json:",inline"`
}

type hiddenHolder struct {
	Visible nestedChild `yaml:"server"`
	hidden  nestedChild `yaml:"hidden"`
}

func (h *hiddenHolder) Validate(fix bool) []error { return nil }

type cycleNode struct {
	Port int        `yaml:"port"`
	Next *cycleNode `yaml:"next"`
}

func (n *cycleNode) Validate(fix bool) []error {
	if n.Port < 0 {
		return []error{errors.New("negative")}
	}
	return nil
}

type portValue int

func (p *portValue) Validate(fix bool) []error {
	if *p <= 0 {
		if fix {
			*p = 8080
		}
		return []error{errors.New("must be > 0")}
	}
	return nil
}

type serverBox struct {
	Port portValue `yaml:"port" json:"port" toml:"port"`
}

type configBox struct {
	Server serverBox `yaml:"server" json:"server" toml:"server"`
}

type ptrHolder struct {
	Child *nestedChild `yaml:"server"`
}

func TestValidate_NestedAndRootBothCalledPostOrder(t *testing.T) {
	var order []string
	cfg := orderedRoot{
		Server: orderedChild{Port: 0, order: &order},
		order:  &order,
	}
	err := Parse([]byte(`{"server":{"port":0}}`), &cfg, WithFormat(FormatJSON), WithAutoFix())
	require.ErrorIs(t, err, ErrValidationFailed)
	assert.Equal(t, []string{"child", "root"}, order)
	assert.Equal(t, 8080, cfg.Server.Port, "child is fixed before parent runs")
}

func TestValidate_PathPrefixedError(t *testing.T) {
	cfg := configBox{Server: serverBox{Port: 0}}
	err := Parse([]byte(`{"server":{"port":0}}`), &cfg, WithFormat(FormatJSON))
	require.ErrorIs(t, err, ErrValidationFailed)
	assert.Contains(t, err.Error(), "server.port")
}

func TestValidate_InlineOmitsSegment(t *testing.T) {
	cfg := inlineWrap{nestedChild: nestedChild{Port: 0}}
	err := Parse([]byte(`{"port":0}`), &cfg, WithFormat(FormatJSON))
	require.ErrorIs(t, err, ErrValidationFailed)
	assert.NotContains(t, err.Error(), "nestedChild")
}

func TestValidate_NilChildSkipped(t *testing.T) {
	cfg := ptrHolder{Child: nil}
	err := Parse([]byte(`{}`), &cfg, WithFormat(FormatJSON))
	require.NoError(t, err)
}

func TestValidate_CycleTerminates(t *testing.T) {
	n := &cycleNode{Port: 1}
	n.Next = n
	err := Validate(n, false)
	require.NoError(t, err)
}

func TestValidate_UnexportedNotCalled(t *testing.T) {
	cfg := hiddenHolder{
		Visible: nestedChild{Port: 1},
		hidden:  nestedChild{Port: 0},
	}
	err := Validate(&cfg, false)
	require.NoError(t, err, "unexported nested Validator must not run")
}

func TestValidate_ExportedFuncAfterOverlay(t *testing.T) {
	cfg := nestedRoot{Server: nestedChild{Port: 0}}
	err := Validate(&cfg, true)
	require.ErrorIs(t, err, ErrValidationFailed)
	assert.Equal(t, 8080, cfg.Server.Port)
	assert.Contains(t, err.Error(), pathStruct)
}

func TestValidate_RejectsNonPointer(t *testing.T) {
	err := Validate(nestedRoot{}, false)
	require.ErrorIs(t, err, ErrInvalidInput)
}

func TestValidate_DashTagSkippedJSON(t *testing.T) {
	type mixed struct {
		Hidden nestedChild `json:"-" yaml:"server"`
		Shown  nestedChild `json:"shown"`
	}
	cfg := mixed{Hidden: nestedChild{Port: 0}, Shown: nestedChild{Port: 1}}
	err := Parse([]byte(`{"shown":{"port":1}}`), &cfg, WithFormat(FormatJSON))
	require.NoError(t, err, "json:\"-\" field must not be walked on JSON parse")
}

func TestValidate_YAMLAndTOMLPaths(t *testing.T) {
	cfg := configBox{Server: serverBox{Port: 0}}
	err := Load("c.yaml", &cfg, WithReader(staticReader([]byte("server:\n  port: 0\n"), nil)))
	require.ErrorIs(t, err, ErrValidationFailed)
	assert.Contains(t, err.Error(), "server.port")

	cfg2 := configBox{Server: serverBox{Port: 0}}
	err = Parse([]byte("[server]\nport = 0\n"), &cfg2, WithFormat(FormatTOML))
	require.ErrorIs(t, err, ErrValidationFailed)
	assert.Contains(t, err.Error(), "server.port")
}

func TestValidate_JSONOmitemptyUsesGoName(t *testing.T) {
	type row struct {
		Port portValue `json:",omitempty"`
	}
	cfg := row{Port: 0}
	err := Parse([]byte(`{"Port":0}`), &cfg, WithFormat(FormatJSON))
	require.ErrorIs(t, err, ErrValidationFailed)
	assert.Contains(t, err.Error(), "Port")
}

func TestValidate_InterfaceAndInvalidValue(t *testing.T) {
	type box struct {
		Inner any `json:"inner"`
	}
	cfg := box{Inner: &nestedChild{Port: 0}}
	err := Validate(&cfg, false)
	require.ErrorIs(t, err, ErrValidationFailed)

	cfg.Inner = nil
	require.NoError(t, Validate(&cfg, false))

	require.NoError(t, callValidator(reflect.Value{}, "f", "", false, FormatAuto))
	_, ok := derefValidate(reflect.Value{}, map[uintptr]bool{})
	assert.False(t, ok)
}

func TestValidate_AutoPrefersJSONWhenNoYAML(t *testing.T) {
	type row struct {
		Port portValue `json:"listen"`
	}
	cfg := row{Port: 0}
	err := Validate(&cfg, false)
	require.ErrorIs(t, err, ErrValidationFailed)
	assert.Contains(t, err.Error(), "listen")
}

func TestValidate_UntaggedGoName(t *testing.T) {
	type row struct {
		Child nestedChild
	}
	cfg := row{Child: nestedChild{Port: 0}}
	err := Validate(&cfg, false)
	require.ErrorIs(t, err, ErrValidationFailed)
	assert.Contains(t, err.Error(), "Child")
}

type valueChild struct {
	N int `json:"n"`
}

func (c valueChild) Validate(fix bool) []error {
	if c.N < 0 {
		return []error{errors.New("neg")}
	}
	return nil
}

func TestValidate_ValueReceiverOnInterface(t *testing.T) {
	type box struct {
		Inner any `json:"inner"`
	}
	cfg := box{Inner: valueChild{N: -1}}
	err := Validate(&cfg, false)
	require.ErrorIs(t, err, ErrValidationFailed)
}

func TestParseStructTag_DashAfterComma(t *testing.T) {
	name, skip, inline := parseStructTag("-,omitempty", "Port")
	assert.True(t, skip)
	assert.False(t, inline)
	assert.Empty(t, name)

	name, skip, inline = parseStructTag("port,omitempty", "Port")
	assert.Equal(t, "port", name)
	assert.False(t, skip)
	assert.False(t, inline)
}

func TestValidate_AutoTOMLTag(t *testing.T) {
	type row struct {
		Port portValue `toml:"listen"`
	}
	cfg := row{Port: 0}
	err := Validate(&cfg, false)
	require.ErrorIs(t, err, ErrValidationFailed)
	assert.Contains(t, err.Error(), "listen")
}

func TestValidate_SliceElementPathAndFix(t *testing.T) {
	type listBox struct {
		Servers []serverBox `json:"servers" yaml:"servers"`
	}
	cfg := listBox{Servers: []serverBox{{Port: 0}, {Port: 1}}}
	err := Parse([]byte(`{"servers":[{"port":0},{"port":1}]}`), &cfg, WithFormat(FormatJSON), WithAutoFix())
	require.ErrorIs(t, err, ErrValidationFailed)
	assert.Contains(t, err.Error(), "servers[0].port")
	assert.NotContains(t, err.Error(), "servers[1]")
	assert.Equal(t, portValue(8080), cfg.Servers[0].Port)
	assert.Equal(t, portValue(1), cfg.Servers[1].Port)
}

func TestValidate_ArrayElementCalled(t *testing.T) {
	type arrBox struct {
		Servers [1]nestedChild `json:"servers"`
	}
	cfg := arrBox{Servers: [1]nestedChild{{Port: 0}}}
	err := Validate(&cfg, true)
	require.ErrorIs(t, err, ErrValidationFailed)
	assert.Contains(t, err.Error(), "servers[0]")
	assert.Equal(t, 8080, cfg.Servers[0].Port)
}

func TestValidate_MapStructAutofixWriteBack(t *testing.T) {
	type mapBox struct {
		Limits map[string]serverBox `json:"limits"`
	}
	cfg := mapBox{Limits: map[string]serverBox{
		"api": {Port: 0},
		"db":  {Port: 9},
	}}
	err := Validate(&cfg, true)
	require.ErrorIs(t, err, ErrValidationFailed)
	assert.Contains(t, err.Error(), "limits[api].port")
	assert.Equal(t, portValue(8080), cfg.Limits["api"].Port)
	assert.Equal(t, portValue(9), cfg.Limits["db"].Port)
}

func TestValidate_MapPointerValues(t *testing.T) {
	type mapBox struct {
		Limits map[string]*nestedChild `json:"limits"`
	}
	cfg := mapBox{Limits: map[string]*nestedChild{"api": {Port: 0}}}
	err := Validate(&cfg, true)
	require.ErrorIs(t, err, ErrValidationFailed)
	assert.Contains(t, err.Error(), "limits[api]")
	assert.Equal(t, 8080, cfg.Limits["api"].Port)
}

func TestValidate_LeafSliceNotDescended(t *testing.T) {
	type row struct {
		Tags []string `json:"tags"`
	}
	cfg := row{Tags: []string{"a", "b"}}
	require.NoError(t, Validate(&cfg, false))
}

func TestValidate_AutoFallsThroughYAMLDash(t *testing.T) {
	type row struct {
		Port portValue `json:"listen" yaml:"-"`
	}
	cfg := row{Port: 0}
	err := Validate(&cfg, false)
	require.ErrorIs(t, err, ErrValidationFailed)
	assert.Contains(t, err.Error(), "listen")
}

func TestValidate_YAMLDashOnlySkipped(t *testing.T) {
	type row struct {
		Hidden nestedChild `yaml:"-"`
		Shown  nestedChild `yaml:"shown"`
	}
	cfg := row{Hidden: nestedChild{Port: 0}, Shown: nestedChild{Port: 1}}
	require.NoError(t, Validate(&cfg, false))
}

func TestValidate_AllCodecDashSkipped(t *testing.T) {
	type row struct {
		Hidden nestedChild `yaml:"-" json:"-" toml:"-"`
	}
	cfg := row{Hidden: nestedChild{Port: 0}}
	require.NoError(t, Validate(&cfg, false))
}

func TestValidate_WithFormatUsesJSONTags(t *testing.T) {
	type row struct {
		Port portValue `json:"listen" yaml:"port"`
	}
	cfg := row{Port: 0}
	err := Validate(&cfg, false, WithFormat(FormatJSON))
	require.ErrorIs(t, err, ErrValidationFailed)
	assert.Contains(t, err.Error(), "listen")
	assert.NotContains(t, err.Error(), "port")
}

func TestValidate_InvalidFormat(t *testing.T) {
	var cfg nestedRoot
	err := Validate(&cfg, false, WithFormat(Format(99)))
	require.ErrorIs(t, err, ErrUnsupportedFormat)
}

func TestValidate_NilOptionIgnored(t *testing.T) {
	cfg := nestedRoot{Server: nestedChild{Port: 1}}
	var none Option
	require.NoError(t, Validate(&cfg, false, none, WithFormat(FormatYAML)))
}

func TestValidate_RootSliceIndexPath(t *testing.T) {
	cfg := []nestedChild{{Port: 0}}
	err := Validate(&cfg, false)
	require.ErrorIs(t, err, ErrValidationFailed)
	assert.Contains(t, err.Error(), "[0]")
}

func TestValidate_MapIntKeys(t *testing.T) {
	type box struct {
		N map[int]nestedChild `json:"n"`
	}
	cfg := box{N: map[int]nestedChild{2: {Port: 0}}}
	err := Validate(&cfg, false)
	require.ErrorIs(t, err, ErrValidationFailed)
	assert.Contains(t, err.Error(), "n[2]")
}

type InnerVal struct {
	Port int `json:"port" yaml:"port"`
}

func (s *InnerVal) Validate(fix bool) []error {
	if s.Port <= 0 {
		if fix {
			s.Port = 8080
		}
		return []error{errors.New("must be > 0")}
	}
	return nil
}

type OuterEmbed struct {
	InnerVal `json:",inline" yaml:",inline"`
}

type OuterNamed struct {
	Child InnerVal `json:"child" yaml:"child"`
}

type OuterOwn struct {
	InnerVal `json:",inline" yaml:",inline"`
}

func (o *OuterOwn) Validate(fix bool) []error {
	return []error{errors.New("own")}
}

type ServerSec struct {
	Port int
}

func (s *ServerSec) Validate(fix bool) []error {
	if s.Port <= 0 {
		if fix {
			s.Port = 8080
		}
		return []error{errors.New("must be > 0")}
	}
	return nil
}

type CfgNilEmbed struct {
	*ServerSec `yaml:",inline" json:",inline"`
}

func TestValidate_ExportedEmbedCalledOnce(t *testing.T) {
	t.Run("report", func(t *testing.T) {
		cfg := OuterEmbed{InnerVal: InnerVal{Port: 0}}
		err := Validate(&cfg, false)
		require.ErrorIs(t, err, ErrValidationFailed)
		assert.Equal(t, 1, strings.Count(err.Error(), "must be > 0"))
	})
	t.Run("autofix", func(t *testing.T) {
		cfg := OuterEmbed{InnerVal: InnerVal{Port: 0}}
		err := Validate(&cfg, true)
		require.ErrorIs(t, err, ErrValidationFailed)
		assert.Equal(t, 1, strings.Count(err.Error(), "must be > 0"))
		assert.Equal(t, 8080, cfg.Port)
	})
}

func TestValidate_NamedFieldStillSingle(t *testing.T) {
	cfg := OuterNamed{Child: InnerVal{Port: 0}}
	err := Validate(&cfg, false)
	require.ErrorIs(t, err, ErrValidationFailed)
	assert.Equal(t, 1, strings.Count(err.Error(), "must be > 0"))
}

func TestValidate_OwnMethodPlusEmbedBothRun(t *testing.T) {
	cfg := OuterOwn{InnerVal: InnerVal{Port: 0}}
	err := Validate(&cfg, false)
	require.ErrorIs(t, err, ErrValidationFailed)
	assert.Contains(t, err.Error(), "must be > 0")
	assert.Contains(t, err.Error(), "own")
}

func TestValidate_NilPointerEmbedDoesNotPanic(t *testing.T) {
	require.NotPanics(t, func() {
		err := Validate(&CfgNilEmbed{}, false)
		require.NoError(t, err)
	})
}

func TestValidate_NilPointerEmbedWithValue(t *testing.T) {
	cfg := CfgNilEmbed{ServerSec: &ServerSec{Port: 0}}
	err := Validate(&cfg, false)
	require.ErrorIs(t, err, ErrValidationFailed)
	assert.Equal(t, 1, strings.Count(err.Error(), "must be > 0"))
}

type ignoredEmbed struct {
	InnerVal `json:"-" yaml:"-"`
}

func TestValidate_IgnoredTagEmbedStillCalled(t *testing.T) {
	cfg := ignoredEmbed{InnerVal: InnerVal{Port: 0}}
	err := Validate(&cfg, false)
	require.ErrorIs(t, err, ErrValidationFailed)
	assert.Equal(t, 1, strings.Count(err.Error(), "must be > 0"),
		"json:\"-\" embed is not walked; parent promotion must still run")
}
