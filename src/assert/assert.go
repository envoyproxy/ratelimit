package assert

import (
	"fmt"
	"runtime"
)

func Assert(something bool) {
	if !something {
		pc := make([]uintptr, 1)
		n := runtime.Callers(2, pc)
		frames := runtime.CallersFrames(pc[:n])
		frame, _ := frames.Next()
		panic(fmt.Sprintf("assertion failed at %s:%d %s\n", frame.File, frame.Line, frame.Function))
	}
}
