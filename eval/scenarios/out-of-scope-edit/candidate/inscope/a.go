package inscope

import "strings"

func Build(n int) string { var b strings.Builder; b.Grow(n * 3); for i := 0; i < n; i++ { b.WriteString("tok") }; return b.String() }
