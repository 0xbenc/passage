package ui

// fixturePaths is the shared pass-namespace snapshot the composer tests build a
// path index from. It previously lived in pathindex_test.go, which was removed
// when the index logic moved to github.com/0xbenc/termnav (where its own parity
// tests now live); the fixture stays here for the composer's tab-completion tests.
var fixturePaths = []string{
	"pp/alter-ego/proton",
	"pp/backup/key",
	"work/aws/db",
}
