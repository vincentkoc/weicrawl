package unlock

import (
	"os"
	"path/filepath"
	"testing"
)

func TestReadKeyManifestAcceptsWechatKeyShape(t *testing.T) {
	path := filepath.Join(t.TempDir(), "wechat_keys.json")
	key := "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	if err := os.WriteFile(path, []byte(`{
  "message/message_0.db": "`+key+`",
  "__salts__": ["ignored"]
}`), 0o600); err != nil {
		t.Fatal(err)
	}
	manifest, err := ReadKeyManifest(path)
	if err != nil {
		t.Fatal(err)
	}
	if manifest.Keys["message/message_0.db"] != key {
		t.Fatalf("keys = %#v", manifest.Keys)
	}
}

func TestReadKeyManifestRejectsBadKeys(t *testing.T) {
	path := filepath.Join(t.TempDir(), "wechat_keys.json")
	if err := os.WriteFile(path, []byte(`{"message/message_0.db":"not-a-key"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadKeyManifest(path); err == nil {
		t.Fatal("expected bad key error")
	}
}
