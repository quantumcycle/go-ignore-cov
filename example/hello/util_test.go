package hello_test

import (
	"testing"

	"github.com/hexira/go-ignore-cov/example/hello"
)

func TestSayHello(t *testing.T) {
	hello.SayHello()
}
