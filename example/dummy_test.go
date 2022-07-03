package example_test

import (
	"testing"

	"github.com/hexira/go-ignore-cov/example"
)

func TestSayHello(t *testing.T) {
	example.MaybeSayHello()
}
