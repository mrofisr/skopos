// Package admission bounds concurrent work per head with an in-flight cap and
// a bounded FIFO wait queue. It never starts goroutines and never blocks a
// release, so a slow loader cannot leak slot accounting.
package admission
