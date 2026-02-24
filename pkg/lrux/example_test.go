package lrux_test

import (
	"fmt"

	"github.com/aasyanov/urx/pkg/lrux"
)

func ExampleNew() {
	cache := lrux.New[string, int](lrux.WithCapacity[string, int](100))
	defer cache.Close()

	cache.Set("hits", 42)

	val, ok := cache.Get("hits")
	fmt.Println(val, ok)
	// Output: 42 true
}
