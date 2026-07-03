package shedx

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// fill admits n requests at the given priority and returns their tokens so the
// caller can hold the shedder at a chosen load. It fails the test if any
// admission is rejected.
func fill(t *testing.T, s *Shedder, priority Priority, n int) []*Token {
	t.Helper()
	tokens := make([]*Token, 0, n)
	for range n {
		tok, err := s.Acquire(priority)
		require.NoError(t, err)
		tokens = append(tokens, tok)
	}
	return tokens
}

func release(tokens []*Token) {
	for _, tok := range tokens {
		tok.Release()
	}
}
