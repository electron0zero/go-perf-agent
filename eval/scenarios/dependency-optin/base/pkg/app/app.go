package app

// App is a trivial in-scope package; the hypothesis targets a vendored dependency outside scope,
// so bench-baseline must ask the user to opt in (need_more_data) rather than validate it.
func App() int { return 1 }
