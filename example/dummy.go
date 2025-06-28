package example

import (
	"fmt"

	"github.com/quantumcycle/go-ignore-cov/example/hello"
)

func MaybeSayHello() {
	// coverage:ignore
	if err, ok := hello.SayHello(); err != nil && ok {
		// coverage:ignore
		fmt.Println("BOOM")
	}
	// coverage:ignore
	fmt.Println("OK")
}

func NotCoveredButIgnored() {
	//coverage:ignore
	fmt.Println("This function is not covered")
}
