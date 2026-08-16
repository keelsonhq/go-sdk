// Example: File upload and download using the Media SDK.
//
// In local mode (no env vars), files are stored on the local filesystem
// under ./media. On Keelson, they go through the Keelson media service.
//
// Run:
//
//	go run ./examples/media-upload
package main

import (
	"fmt"
	"io"
	"log"
	"strings"

	"github.com/keelsonhq/go-sdk/media"
)

func main() {
	client, err := media.New("", "")
	if err != nil {
		log.Fatal(err)
	}

	if client.IsLocal() {
		fmt.Println("running in local mode (files stored on disk)")
	}

	// Upload a text file.
	fileID := "example-hello.txt"
	err = client.Put(fileID, strings.NewReader("Hello from Keelson!"),
		media.WithContentType("text/plain"),
		media.WithFilename("hello.txt"),
	)
	if err != nil {
		log.Fatalf("put: %v", err)
	}
	fmt.Printf("uploaded: %s\n", fileID)

	// Check existence.
	exists, err := client.Exists(fileID)
	if err != nil {
		log.Fatalf("exists: %v", err)
	}
	fmt.Printf("exists: %v\n", exists)

	// Get metadata.
	meta, err := client.Head(fileID)
	if err != nil {
		log.Fatalf("head: %v", err)
	}
	fmt.Printf("content-type: %s, size: %d bytes\n", meta.ContentType, meta.ContentLength)

	// Download.
	content, err := client.Get(fileID)
	if err != nil {
		log.Fatalf("get: %v", err)
	}
	defer content.Close()

	data, _ := io.ReadAll(content.Body)
	fmt.Printf("content: %s\n", string(data))

	// Public URL path.
	fmt.Printf("url: %s\n", client.URL(fileID))

	// Cleanup.
	if err := client.Delete(fileID); err != nil {
		log.Fatalf("delete: %v", err)
	}
	fmt.Println("deleted")
}
