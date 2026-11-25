package supervisor

// recycled makes fp stand for a process that held its pid before: one
// that started a microsecond earlier.
func recycled(fp *foreignProcess) { fp.start.Usec-- }
