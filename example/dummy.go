package example

import (
	"fmt"

	"github.com/hexira/go-ignore-cov/example/hello"
)

//this package should have 100% code coverage if we remove the ignored statements
func MaybeSayHello() {
	fmt.Println("Maybe Maybe Maybe")
	if err, ok := hello.SayHello(); err != nil && ok {
		// coverage:ignore
		fmt.Println("BOOM")
	}
}
