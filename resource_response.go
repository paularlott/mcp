package mcp

import "encoding/base64"

func NewResourceResponseText(uri, text, mimeType string) *ResourceResponse {
	return &ResourceResponse{
		Contents: []ResourceContent{{
			URI:      uri,
			Text:     text,
			MimeType: mimeType,
		}},
	}
}

func NewResourceResponseBlob(uri string, data []byte, mimeType string) *ResourceResponse {
	return &ResourceResponse{
		Contents: []ResourceContent{{
			URI:      uri,
			Blob:     base64.StdEncoding.EncodeToString(data),
			MimeType: mimeType,
		}},
	}
}
