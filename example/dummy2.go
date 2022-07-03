// coverage:ignore file
package example

import "fmt"

func IgnoredFunction(name string) {
	callout := fmt.Sprintf("Hello %s", name)
	if name == "World" {
		fmt.Println("Seriously?!")
		return
	}
	fmt.Println(callout)
}
