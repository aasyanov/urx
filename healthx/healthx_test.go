package healthx

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/aasyanov/urx/internal/testx"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func upCheck(context.Context) error   { return nil }
func downCheck(context.Context) error { return errors.New("component failed") }

func TestNew_Defaults(t *testing.T) {
	c := New()
	assert.Equal(t, defaultCheckTimeout, c.cfg.checkTimeout)
}

func TestWithTimeout(t *testing.T) {
	tests := []struct {
		name string
		opt  Option
		want time.Duration
	}{
		{name: "default", opt: nil, want: defaultCheckTimeout},
		{name: "custom", opt: WithTimeout(2 * time.Second), want: 2 * time.Second},
		{name: "zero ignored", opt: WithTimeout(0), want: defaultCheckTimeout},
		{name: "negative ignored", opt: WithTimeout(-time.Second), want: defaultCheckTimeout},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var opts []Option
			if tt.opt != nil {
				opts = append(opts, tt.opt)
			}
			c := New(opts...)
			assert.Equal(t, tt.want, c.cfg.checkTimeout)
		})
	}
}

func TestRegister_NilCheckPanics(t *testing.T) {
	c := New()
	assert.Panics(t, func() {
		c.Register("bad", nil)
	})
}

func TestRegister_IncrementsStats(t *testing.T) {
	c := New()
	c.Register("a", upCheck)
	c.Register("b", upCheck)
	assert.Equal(t, 2, c.Stats().Registered)
}

func TestLiveness_UpByDefault(t *testing.T) {
	c := New()
	rep := c.Liveness(context.Background())
	assert.Equal(t, StatusUp, rep.Status)
	assert.Equal(t, zeroDuration, rep.Duration)
	assert.Nil(t, rep.Components)
}

func TestLiveness_DownAfterMarkDown(t *testing.T) {
	c := New()
	c.MarkDown()
	assert.True(t, c.IsDown())
	assert.Equal(t, StatusDown, c.Liveness(context.Background()).Status)

	c.MarkUp()
	assert.False(t, c.IsDown())
	assert.Equal(t, StatusUp, c.Liveness(context.Background()).Status)
}

func TestReadiness_EmptyIsUp(t *testing.T) {
	c := New()
	rep := c.Readiness(context.Background())
	assert.Equal(t, StatusUp, rep.Status)
	assert.Empty(t, rep.Components)
}

func TestReadiness_NilContext(t *testing.T) {
	c := New()
	c.Register("a", upCheck)
	rep := c.Readiness(nil) //nolint:staticcheck // intentionally exercising nil-ctx guard
	assert.Equal(t, StatusUp, rep.Status)
}

func TestReadiness_AllUp(t *testing.T) {
	c := New()
	c.Register("a", upCheck)
	c.Register("b", upCheck)

	rep := c.Readiness(context.Background())
	assert.Equal(t, StatusUp, rep.Status)
	require.Len(t, rep.Components, 2)
	assert.Equal(t, StatusUp, rep.Components["a"].Status)
	assert.Equal(t, StatusUp, rep.Components["b"].Status)
}

func TestReadiness_OneDownFailsOverall(t *testing.T) {
	c := New()
	c.Register("ok", upCheck)
	c.Register("bad", downCheck)

	rep := c.Readiness(context.Background())
	assert.Equal(t, StatusDown, rep.Status)
	assert.Equal(t, StatusUp, rep.Components["ok"].Status)
	assert.Equal(t, StatusDown, rep.Components["bad"].Status)
	assert.Contains(t, rep.Components["bad"].Error, "component failed")
}

func TestReadiness_FailureWrapsErrUnhealthy(t *testing.T) {
	c := New()
	c.Register("bad", downCheck)

	rep := c.Readiness(context.Background())
	assert.Contains(t, rep.Components["bad"].Error, ErrUnhealthy.Error())
}

func TestReadiness_TimeoutWrapsErrTimeout(t *testing.T) {
	c := New(WithTimeout(20 * time.Millisecond))
	slow := testx.SlowCall(2 * time.Second)
	c.Register("slow", slow.Call)

	rep := c.Readiness(context.Background())
	assert.Equal(t, StatusDown, rep.Status)
	assert.Equal(t, StatusDown, rep.Components["slow"].Status)
	assert.Contains(t, rep.Components["slow"].Error, ErrTimeout.Error())
}

func TestReadiness_ParentCancelIsUnhealthyNotTimeout(t *testing.T) {
	c := New(WithTimeout(2 * time.Second))
	slow := testx.SlowCall(2 * time.Second)
	c.Register("slow", slow.Call)

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // parent cancelled before the check runs

	rep := c.Readiness(ctx)
	assert.Equal(t, StatusDown, rep.Status)
	cs := rep.Components["slow"]
	assert.Equal(t, StatusDown, cs.Status)
	assert.Contains(t, cs.Error, ErrUnhealthy.Error(), "parent cancel is a generic failure")
	assert.NotContains(t, cs.Error, ErrTimeout.Error(), "parent cancel must not be classified as a check timeout")
}

func TestReadiness_ContextIgnoringCheckDoesNotHang(t *testing.T) {
	c := New(WithTimeout(50 * time.Millisecond))
	blocked := make(chan struct{})
	c.Register("ok", upCheck)
	c.Register("rogue", func(context.Context) error {
		<-blocked // deliberately ignores ctx — must not wedge the probe
		return nil
	})

	done := make(chan Report, 1)
	go func() { done <- c.Readiness(context.Background()) }()

	select {
	case rep := <-done:
		assert.Equal(t, StatusDown, rep.Status)
		assert.Equal(t, StatusUp, rep.Components["ok"].Status)
		assert.Equal(t, StatusDown, rep.Components["rogue"].Status)
		assert.Contains(t, rep.Components["rogue"].Error, ErrTimeout.Error())
	case <-time.After(2 * time.Second):
		t.Fatal("Readiness hung on a context-ignoring check")
	}
	close(blocked)
}

func TestReadiness_PanicRecovered(t *testing.T) {
	c := New()
	c.Register("boom", func(context.Context) error { panic("check exploded") })

	var rep Report
	assert.NotPanics(t, func() {
		rep = c.Readiness(context.Background())
	})
	assert.Equal(t, StatusDown, rep.Status)
	assert.Equal(t, StatusDown, rep.Components["boom"].Status)
	assert.Contains(t, rep.Components["boom"].Error, ErrUnhealthy.Error())
}

func TestReadiness_MarkDownSkipsChecks(t *testing.T) {
	c := New()
	var ran bool
	c.Register("a", func(context.Context) error { ran = true; return nil })
	c.MarkDown()

	rep := c.Readiness(context.Background())
	assert.Equal(t, StatusDown, rep.Status)
	assert.Nil(t, rep.Components)
	assert.False(t, ran, "checks must not run while marked down")
}

func TestReadiness_CheckReceivesDeadline(t *testing.T) {
	c := New(WithTimeout(time.Second))
	var sawDeadline bool
	c.Register("a", func(ctx context.Context) error {
		_, sawDeadline = ctx.Deadline()
		return nil
	})

	c.Readiness(context.Background())
	assert.True(t, sawDeadline, "each check must run under the per-check timeout")
}

func TestStats_TracksReadiness(t *testing.T) {
	c := New()
	c.Register("ok", upCheck)
	c.Register("bad", downCheck)

	c.Readiness(context.Background())
	c.Readiness(context.Background())

	st := c.Stats()
	assert.Equal(t, uint64(2), st.ReadinessChecks)
	assert.Equal(t, uint64(2), st.ReadinessFailures)
	assert.Equal(t, 2, st.Registered)
	assert.False(t, st.Down)
}

func TestStats_MarkDownReflected(t *testing.T) {
	c := New()
	c.MarkDown()
	assert.True(t, c.Stats().Down)
}

func TestResetStats(t *testing.T) {
	c := New()
	c.Register("bad", downCheck)
	c.Readiness(context.Background())
	require.Positive(t, c.Stats().ReadinessChecks)

	c.ResetStats()
	st := c.Stats()
	assert.Equal(t, uint64(0), st.ReadinessChecks)
	assert.Equal(t, uint64(0), st.ReadinessFailures)
	assert.Equal(t, 1, st.Registered, "ResetStats must not drop registered checks")
}

func TestLiveHandler(t *testing.T) {
	c := New()
	srv := httptest.NewServer(c.LiveHandler())
	defer srv.Close()

	resp, err := http.Get(srv.URL)
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, "application/json", resp.Header.Get("Content-Type"))

	c.MarkDown()
	resp2, err := http.Get(srv.URL)
	require.NoError(t, err)
	defer resp2.Body.Close()
	assert.Equal(t, http.StatusServiceUnavailable, resp2.StatusCode)
}

func TestReadyHandler(t *testing.T) {
	c := New()
	c.Register("ok", upCheck)
	srv := httptest.NewServer(c.ReadyHandler())
	defer srv.Close()

	resp, err := http.Get(srv.URL)
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusOK, resp.StatusCode)

	c.Register("bad", downCheck)
	resp2, err := http.Get(srv.URL)
	require.NoError(t, err)
	defer resp2.Body.Close()
	assert.Equal(t, http.StatusServiceUnavailable, resp2.StatusCode)
}

func TestRegisterHandlers(t *testing.T) {
	c := New()
	c.Register("ok", upCheck)
	mux := http.NewServeMux()
	c.RegisterHandlers(mux)
	srv := httptest.NewServer(mux)
	defer srv.Close()

	for _, path := range []string{routeHealthz, routeLivez, routeReadyz} {
		resp, err := http.Get(srv.URL + path)
		require.NoError(t, err, path)
		require.NoError(t, resp.Body.Close())
		assert.Equal(t, http.StatusOK, resp.StatusCode, path)
	}
}

func TestRegisterHandlers_NilMuxPanics(t *testing.T) {
	c := New()
	assert.Panics(t, func() {
		c.RegisterHandlers(nil)
	})
}

func TestReadiness_ConcurrentWithRegister(t *testing.T) {
	c := New()
	c.Register("base", upCheck)

	done := make(chan struct{})
	go func() {
		for i := 0; i < 100; i++ {
			c.Register("dynamic", upCheck)
		}
		close(done)
	}()

	testx.HammerVoid(20, 50, func() {
		c.Readiness(context.Background())
	})
	<-done
	assert.Positive(t, c.Stats().ReadinessChecks)
}
