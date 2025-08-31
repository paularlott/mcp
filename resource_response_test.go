package mcp

import (
	"encoding/base64"
	"testing"
)

func TestResourceResponseHelpers(t *testing.T) {
	txt := NewResourceResponseText("file://x", "hello", "text/plain")
	if len(txt.Contents) != 1 || txt.Contents[0].Text != "hello" || txt.Contents[0].MimeType != "text/plain" {
		t.Fatalf("unexpected text resource: %+v", txt)
	}
	data := []byte{0x01, 0x02}
	blob := NewResourceResponseBlob("file://y", data, "application/octet-stream")
	if blob.Contents[0].Blob != base64.StdEncoding.EncodeToString(data) || blob.Contents[0].URI != "file://y" {
		t.Fatalf("unexpected blob resource: %+v", blob)
	}
}
