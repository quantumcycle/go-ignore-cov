package example

import "fmt"

//this package should have 100% code coverage if we remove the ignored statements

func sayHello() (error, bool) {
	fmt.Println("Hello")
	return nil, true
}

func MaybeSayHello() {
	fmt.Println("Maybe Maybe Maybe")
	if err, ok := sayHello(); err != nil && ok {
		// coverage:ignore
		fmt.Println("BOOM")
	}
}
