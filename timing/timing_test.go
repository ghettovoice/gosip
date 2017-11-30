package timing

// Tests for the mock timing module.

import (
	"testing"
	"time"
)

func TestTimer(t *testing.T) {
	MockMode = true
	timer := NewTimer(5 * time.Second)
	done := make(chan struct{})

	go func() {
		<-timer.C()
		done <- struct{}{}
	}()

	Elapse(5 * time.Second)
	<-done
}

func TestTwoTimers(t *testing.T) {
	MockMode = true
	timer1 := NewTimer(5 * time.Second)
	done1 := make(chan struct{})

	timer2 := NewTimer(5 * time.Millisecond)
	done2 := make(chan struct{})

	go func() {
		<-timer1.C()
		done1 <- struct{}{}
	}()

	go func() {
		<-timer2.C()
		done2 <- struct{}{}
	}()

	Elapse(5 * time.Millisecond)
	<-done2

	Elapse(9995 * time.Millisecond)
	<-done1
}

func TestAfter(t *testing.T) {
	MockMode = true
	done := make(chan struct{})
	c := After(5 * time.Second)

	go func() {
		<-c
		done <- struct{}{}
	}()

	Elapse(5 * time.Second)
	<-done
}

func TestAfterFunc(t *testing.T) {
	MockMode = true
	done := make(chan struct{})
	AfterFunc(5*time.Second,
		func() {
			done <- struct{}{}
		})

	Elapse(5 * time.Second)
	<-done
}

func TestAfterFuncReset(t *testing.T) {
	MockMode = true
	done := make(chan struct{})
	timer := AfterFunc(5*time.Second,
		func() {
			done <- struct{}{}
		})

	Elapse(3 * time.Second)
	timer.Reset(5 * time.Second)
	Elapse(2 * time.Second)

	select {
	case <-done:
		t.Fatal("AfterFunc fired at its old end time after being reset.")
	case <-time.After(50 * time.Millisecond):
		t.Log("AfterFunc correctly didn't fire at its old end time after being reset.")
	}

	Elapse(3 * time.Second)
	select {
	case <-done:
		t.Log("AfterFunc correctly fired at its new end time after being reset.")
	case <-time.After(50 * time.Millisecond):
		t.Fatal("AfterFunc didn't fire at its new end time after being reset.")
	}
}

func TestAfterFuncExpiredReset(t *testing.T) {
	MockMode = true
	done := make(chan struct{})
	timer := AfterFunc(5*time.Second,
		func() {
			done <- struct{}{}
		})

	Elapse(5 * time.Second)

	select {
	case <-done:
		t.Log("AfterFunc correctly fired at its end time.")
	case <-time.After(50 * time.Millisecond):
		t.Fatal("AfterFunc didn't fire at its end time")
	}

	timer.Reset(5 * time.Second)
	Elapse(5 * time.Second)
	select {
	case <-done:
		t.Log("AfterFunc correctly fired at its new end time after being reset.")
	case <-time.After(50 * time.Millisecond):
		t.Fatal("AfterFunc didn't fire at its new end time after being reset.")
	}
}

func TestExpiredReset(t *testing.T) {
	MockMode = true
	timer := NewTimer(5 * time.Second)
	done := make(chan struct{})

	go func() {
		<-timer.C()
		done <- struct{}{}
	}()

	Elapse(5 * time.Second)
	<-done

	timer.Reset(3 * time.Second)
	go func() {
		<-timer.C()
		done <- struct{}{}
	}()

	Elapse(2 * time.Second)
	select {
	case <-done:
		t.Fatal("Timer fired at its old end time after being reset.")
	case <-time.After(50 * time.Millisecond):
		t.Log("Timer correctly didn't fire at its old end time after being reset.")
	}

	Elapse(1 * time.Second)
	select {
	case <-done:
		t.Log("Timer correctly fired at its new end time after being reset.")
	case <-time.After(50 * time.Millisecond):
		t.Fatal("Timer didn't fire at its new end time after being reset.")
	}
}

func TestNotExpiredReset(t *testing.T) {
	MockMode = true
	timer := NewTimer(5 * time.Second)
	done := make(chan struct{})

	go func() {
		<-timer.C()
		done <- struct{}{}
	}()

	Elapse(4 * time.Second)
	timer.Reset(5 * time.Second)
	Elapse(1 * time.Second)

	select {
	case <-done:
		t.Fatal("Timer fired at its old end time after being reset.")
	case <-time.After(50 * time.Millisecond):
		t.Log("Timer correctly didn't fire at its old end time after being reset.")
	}

	Elapse(4 * time.Second)
	select {
	case <-done:
		t.Log("Timer correctly fired at its new end time after being reset.")
	case <-time.After(50 * time.Millisecond):
		t.Fatal("Timer didn't fire at its new end time after being reset.")
	}
}

// This is a regression test for a bug where:
//  - Create 3 timers.
//  - Reset() the first one.
//  - The third timer is now no longer tracked and won't fire.
func TestThreeTimersWithReset(t *testing.T) {
	MockMode = true
	timer1 := NewTimer(1 * time.Second)
	done1 := make(chan struct{})

	timer2 := NewTimer(2 * time.Second)
	done2 := make(chan struct{})

	timer3 := NewTimer(3 * time.Second)
	done3 := make(chan struct{})

	go func() {
		<-timer1.C()
		done1 <- struct{}{}
	}()

	go func() {
		<-timer2.C()
		done2 <- struct{}{}
	}()

	go func() {
		<-timer3.C()
		done3 <- struct{}{}
	}()

	timer1.Reset(4 * time.Second)

	Elapse(2 * time.Second)
	<-done2

	Elapse(1 * time.Second)
	// Panic here if bug exists.
	<-done3
}
