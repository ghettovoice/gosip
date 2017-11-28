package main

import (
	"fmt"
	"time"
)

func main() {
	done := make(chan bool, 1)

	go func() {
		for {
			select {
			case <-done:
				fmt.Println("goroutine 1 stop")
				return
			default:
				//fmt.Println("goroutine 1 next")
			}
		}
	}()

	go func() {
		for {
			select {
			case <-done:
				fmt.Println("goroutine 2 stop")
				return
			default:
				//fmt.Println("goroutine 2 next")
			}
		}
	}()

	fmt.Println("goroutines stared")

	<-time.After(500 * time.Millisecond)
	done <- true

	fmt.Println("done")
}
