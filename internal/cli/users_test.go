package cli

import (
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// madeUpPassword stands for a password Geass made. It is built from parts so that secret scanners do not take it for one.
var madeUpPassword = strings.Join([]string{"ABCDE", "FGHJK", "LMNPQ", "RSTUV"}, "-")

func usersStub() *computeStub {
	stub := newComputeStub()
	stub.reply("GET /users", 200, map[string]any{"items": []map[string]any{
		{"username": "admin", "role": "admin"},
		{"username": "mara", "role": "viewer"},
	}})
	stub.on("POST /users", func(w http.ResponseWriter, req sentRequest) {
		answer := map[string]any{"ok": true, "username": "ops", "role": "admin"}
		if _, chosen := req.body["password"]; !chosen {
			answer["password"] = madeUpPassword
		}
		reply(w, 200, answer)
	})
	stub.reply("POST /users/mara/role", 200, map[string]any{"ok": true})
	stub.reply("POST /users/mara/password", 200, map[string]any{"ok": true, "password": madeUpPassword})
	stub.reply("DELETE /users/mara", 200, map[string]any{"ok": true})
	stub.reply("POST /users/sign-out-all", 200, map[string]any{"ok": true})
	return stub
}

func TestUsersAreListedWithTheirRoles(t *testing.T) {
	h := newHarness(t, usersStub().handler(t))
	h.signedIn(t, "", "")

	code, stdout, stderr := h.run("users", "list")

	require.Equal(t, ExitOK, code, stderr)
	require.Regexp(t, `USERNAME\s+ROLE`, stdout)
	require.Regexp(t, `admin\s+admin`, stdout)
	require.Regexp(t, `mara\s+viewer`, stdout)
}

func TestUsersCanBeListedForAProgram(t *testing.T) {
	h := newHarness(t, usersStub().handler(t))
	h.signedIn(t, "", "")

	code, stdout, _ := h.run("--json", "users", "list")

	require.Equal(t, ExitOK, code)
	require.Contains(t, stdout, `"items"`)
	require.Contains(t, stdout, `"mara"`)
}

func TestAUserIsAddedWithAPasswordThatIsPrintedOnce(t *testing.T) {
	stub := usersStub()
	h := newHarness(t, stub.handler(t))
	h.signedIn(t, "", "")

	code, stdout, stderr := h.run("users", "add", "ops", "--role", "admin")

	require.Equal(t, ExitOK, code, stderr)
	require.Equal(t, madeUpPassword+"\n", stdout, "only the password is on standard output, so a script can take it")
	require.Contains(t, stderr, "shown once")
	sent := stub.sent("POST", "/users")
	require.Len(t, sent, 1)
	require.Equal(t, map[string]any{"username": "ops", "role": "admin"}, sent[0].body, "no password is asked for: Geass makes one")
}

func TestAPasswordIsReadFromAFileOrStandardInputAndNeverAnArgument(t *testing.T) {
	stub := usersStub()
	h := newHarness(t, stub.handler(t))
	h.signedIn(t, "", "")
	h.app.In = strings.NewReader("my-own-long-password\n")

	code, stdout, stderr := h.run("users", "add", "ops", "--password-file", "-")

	require.Equal(t, ExitOK, code, stderr)
	require.Contains(t, stdout, "Added ops.")
	require.NotContains(t, stdout+stderr, "my-own-long-password", "a password the person chose is not echoed")
	require.Equal(t, "my-own-long-password", stub.sent("POST", "/users")[0].body["password"], "the newline of the file is not part of it")

	code, _, stderr = h.run("users", "add", "ops", "--password", "x")
	require.Equal(t, ExitUsage, code, "there is no flag that takes the password itself: %s", stderr)
}

func TestARoleIsChecked(t *testing.T) {
	stub := usersStub()
	h := newHarness(t, stub.handler(t))
	h.signedIn(t, "", "")

	code, _, stderr := h.run("users", "add", "ops", "--role", "owner")
	require.Equal(t, ExitUsage, code)
	require.Contains(t, stderr, "admin or viewer")
	code, _, stderr = h.run("users", "set-role", "mara", "observer")
	require.Equal(t, ExitUsage, code)
	require.Contains(t, stderr, "admin or viewer")
	require.Empty(t, stub.writes(), "nothing was sent")

	code, stdout, stderr := h.run("users", "set-role", "mara", "ADMIN")
	require.Equal(t, ExitOK, code, stderr)
	require.Contains(t, stdout, "mara is now an admin.")
	require.Equal(t, map[string]any{"role": "admin"}, stub.sent("POST", "/users/mara/role")[0].body)
}

func TestAPasswordIsResetAndPrintedOnce(t *testing.T) {
	stub := usersStub()
	h := newHarness(t, stub.handler(t))
	h.signedIn(t, "", "")

	code, stdout, stderr := h.run("users", "reset-password", "mara")

	require.Equal(t, ExitOK, code, stderr)
	require.Equal(t, madeUpPassword+"\n", stdout)
	require.Contains(t, stderr, "Reset the password of mara")
}

func TestRemovingAUserAndSigningEveryoneOutAskFirst(t *testing.T) {
	stub := usersStub()
	h := newHarness(t, stub.handler(t))
	h.signedIn(t, "", "")

	code, _, stderr := h.run("users", "remove", "mara")
	require.Equal(t, ExitUsage, code, "with nobody to ask, it needs --yes: %s", stderr)
	code, _, _ = h.run("users", "sign-out-all")
	require.Equal(t, ExitUsage, code)
	require.Empty(t, stub.writes())

	code, stdout, stderr := h.run("--yes", "users", "remove", "mara")
	require.Equal(t, ExitOK, code, stderr)
	require.Contains(t, stdout, "Removed the user mara.")
	code, stdout, stderr = h.run("--yes", "users", "sign-out-all")
	require.Equal(t, ExitOK, code, stderr)
	require.Contains(t, stdout, "Signed everyone out")
	require.Len(t, stub.sent(http.MethodDelete, "/users/mara"), 1)
	require.Len(t, stub.sent(http.MethodPost, "/users/sign-out-all"), 1)
}

func TestAUserTheServerRefusesIsReportedWithItsReason(t *testing.T) {
	stub := usersStub()
	stub.reply("DELETE /users/admin", 409, map[string]any{"error": "this is the last admin: make another user an admin first", "code": "conflict"})
	h := newHarness(t, stub.handler(t))
	h.signedIn(t, "", "")

	code, _, stderr := h.run("--yes", "users", "remove", "admin")

	require.NotEqual(t, ExitOK, code)
	require.Contains(t, stderr, "last admin")
}
