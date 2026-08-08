package main

import "testing"

func TestParseWordCommands(t *testing.T) {
	parsed, err := parseOptions([]string{"docker", "/workspace"}, false)
	if err != nil {
		t.Fatal(err)
	}
	if parsed.command != commandDocker || parsed.workspace != "/workspace" {
		t.Fatalf("unexpected Docker command: %#v", parsed)
	}
	parsed, err = parseOptions([]string{"vm"}, false)
	if err != nil || parsed.command != commandVM {
		t.Fatalf("unexpected VM command: %#v %v", parsed, err)
	}
}

func TestFlagsAndUnknownCommandsAreRejected(t *testing.T) {
	for _, arguments := range [][]string{{"--docker"}, {"preview"}} {
		if _, err := parseOptions(arguments, false); err == nil {
			t.Fatalf("obsolete arguments were accepted: %v", arguments)
		}
	}
}

func TestHelpAliases(t *testing.T) {
	for _, alias := range []string{"help", "-h", "--help"} {
		parsed, err := parseOptions([]string{alias}, false)
		if err != nil || parsed.command != commandHelp {
			t.Fatalf("help alias %q was rejected: %#v %v", alias, parsed, err)
		}
	}
	if _, err := parseOptions([]string{"--help", "extra"}, false); err == nil {
		t.Fatal("help alias accepted an additional argument")
	}
}

func TestRunCommandIsContainerOnly(t *testing.T) {
	if _, err := parseOptions([]string{"run", "/workspace"}, false); err == nil {
		t.Fatal("internal run command was accepted on the host")
	}
	parsed, err := parseOptions([]string{"run", "/workspace"}, true)
	if err != nil || parsed.workspace != "/workspace" {
		t.Fatalf("container run command was rejected: %#v %v", parsed, err)
	}
}
