package hello

import (
	"fmt"
)

func SayHello() (error, bool) {
	fmt.Println("Hello")
	return nil, true
}

