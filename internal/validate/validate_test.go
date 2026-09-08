package validate

import (
	"errors"
	"strings"
	"testing"
)

func TestUserIDAcceptsValid(t *testing.T) {
	for _, id := range []string{
		"dev-user",
		"integration_test_user",
		"a",
		strings.Repeat("x", MaxIDLength),
	} {
		if err := UserID(id); err != nil {
			t.Errorf("UserID(%q) unexpectedly rejected: %v", id, err)
		}
	}
}

func TestUserIDRejectsInvalid(t *testing.T) {
	for _, id := range []string{
		"",
		strings.Repeat("x", MaxIDLength+1),
		"with space",
		"user/id",
		"user..id",
		"user@twitch",
		"../etc/passwd",
		"http://twitch.tv/xqc",
		"ünïcode",
		"x\n",
	} {
		if err := UserID(id); err == nil {
			t.Errorf("UserID(%q) unexpectedly accepted", id)
		}
	}
}

func TestStreamIDRules(t *testing.T) {
	if err := StreamID("xqc"); err != nil {
		t.Errorf("StreamID(xqc) rejected: %v", err)
	}
	if err := StreamID("xqc/extra"); err == nil {
		t.Error("StreamID(xqc/extra) unexpectedly accepted")
	}
}

func TestErrorIsSentinels(t *testing.T) {
	if !errors.Is(UserID(""), ErrInvalidUser) {
		t.Error("expected UserID error to wrap ErrInvalidUser")
	}
	if !errors.Is(StreamID("a b"), ErrInvalidStream) {
		t.Error("expected StreamID error to wrap ErrInvalidStream")
	}
}
