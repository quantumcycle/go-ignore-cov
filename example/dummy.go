package example

import (
	"context"
	"fmt"
	"github.com/quantumcycle/go-ignore-cov/example/hello"
	"log"
)

func CreateAllComponents(ctx context.Context) []string {

	// Catch any panics and convert them to proper log.Fatal calls
	//coverage:ignore
	defer func() {
		//coverage:ignore
		if r := recover(); r != nil {
			//coverage:ignore
			log.Printf("FATAL: Component initialization panicked with error: %+v", r)
			log.Fatalf("Application terminated due to panic: %+v", r)
		}
	}()

	//coverage:ignore
	log.Printf("Creating users component...")
	return nil
}

func MaybeSayHello() {
	defer func() {
		//coverage:ignore
		if r := recover(); r != nil {
			//coverage:ignore
			log.Fatalf("Application terminated due to panic: %+v", r)
		}
	}()

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
