// Package tests holds the end-to-end tests of Sift: they drive the
// application layer and the command line against real corpora in temporary
// directories, so every stage from scanning to publishing to searching is
// exercised together rather than in isolation.
//
// The package deliberately contains no code of its own. Everything it needs
// lives in the packages under test.
package tests
