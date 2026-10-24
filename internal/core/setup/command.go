package setup

import "github.com/wloophq/wloop/internal/domain/shell"

// InstallCommand is the command that installs the loop into one home.
func InstallCommand(command, home string) string {
	return command + " install --home " + shell.Word(home)
}

// TrustCommand is the command that trusts the repository of folder in home.
func TrustCommand(command, home, folder string) string {
	return command + " trust --home " + shell.Word(home) + " " + shell.Word(folder)
}
