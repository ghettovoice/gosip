package utils

import (
	"bytes"
	"runtime/debug"
)

func GetStack(skipFrames int) []byte {
	stack := debug.Stack()
	lines := bytes.Split(stack, []byte("\n"))
	lines[0] = append(lines[0], []byte("\n")...)
	skip := 2*(skipFrames+2) + 1 // 2 lines on each frame + 2 lines on debug.Stack() + 2 lines on this func + 1
	return append(lines[0], bytes.Join(lines[skip:], []byte("\n"))...)
}

func GetStack4() any { return GetStack(4) }
