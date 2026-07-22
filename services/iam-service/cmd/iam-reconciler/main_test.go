package main

import (
	"strings"
	"testing"
)

func TestDecodeCommandRejectsUnknownFieldsAndMultipleObjects(t *testing.T) {
	for name, input := range map[string]string{
		"unknown":  "{\"userId\":\"user-123\",\"seed\":{},\"unexpected\":true}",
		"multiple": "{\"userId\":\"user-123\",\"seed\":{}} {\"userId\":\"user-456\",\"seed\":{}}",
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := decodeCommand(strings.NewReader(input)); err == nil {
				t.Fatalf("invalid command was accepted: %s", input)
			}
		})
	}
}

func TestDecodeCommandTrimsAndRequiresUserID(t *testing.T) {
	command, err := decodeCommand(strings.NewReader("{\"userId\":\" user-123 \",\"seed\":{}}"))
	if err != nil {
		t.Fatal(err)
	}
	if command.UserID != "user-123" {
		t.Fatalf("user id was not normalized: %q", command.UserID)
	}
	if _, err := decodeCommand(strings.NewReader("{\"userId\":\" \",\"seed\":{}}")); err == nil {
		t.Fatal("blank user id was accepted")
	}
}

func TestRequiredEnvironmentRejectsBlankValue(t *testing.T) {
	getenv := func(name string) string {
		if name == "PRESENT" {
			return " value "
		}
		return ""
	}
	value, err := requiredEnvironment(getenv, "PRESENT")
	if err != nil || value != "value" {
		t.Fatalf("unexpected environment value: %q %v", value, err)
	}
	if _, err := requiredEnvironment(getenv, "MISSING"); err == nil {
		t.Fatal("missing environment variable was accepted")
	}
}
