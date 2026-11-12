package turns

import (
	"reflect"
	"testing"
)

func TestTurnDoneByApproval(t *testing.T) {
	t.Skip("T9")
}

func TestFailedSendIsUnitFact(t *testing.T) {
	t.Skip("T9")
}

func TestSourceWritesFalseDeniesManifestCommands(t *testing.T) {
	t.Skip("T9")
}

func TestNoGitHubWritesRemoved(t *testing.T) {
	if _, ok := reflect.TypeFor[Policy]().FieldByName("NoGitHubWrites"); ok {
		t.Fatal("a session policy names no provider: the manifests' write_commands deny their writes")
	}
}
