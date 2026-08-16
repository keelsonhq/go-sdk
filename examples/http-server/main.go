// Example: HTTP server that uses Identity, Directory, and Media SDKs.
//
// This shows a typical Keelson app pattern: an HTTP handler that identifies
// the current user through the Keelson auth gateway, lists workspace members,
// and serves files.
//
// Run:
//
//	go run ./examples/http-server
package main

import (
	"fmt"
	"io"
	"log"
	"net/http"

	"github.com/keelsonhq/go-sdk/directory"
	"github.com/keelsonhq/go-sdk/identity"
	"github.com/keelsonhq/go-sdk/media"
)

func main() {
	idClient, err := identity.New("")
	if err != nil {
		log.Fatal(err)
	}

	dirClient, err := directory.New("")
	if err != nil {
		log.Fatal(err)
	}

	mediaClient, err := media.New("", "")
	if err != nil {
		log.Fatal(err)
	}

	http.HandleFunc("/me", func(w http.ResponseWriter, r *http.Request) {
		me, err := idClient.GetCurrentUser(
			identity.WithHeaders(r.Header),
		)
		if err != nil {
			http.Error(w, err.Error(), http.StatusUnauthorized)
			return
		}
		email := ""
		if me.Email != nil {
			email = *me.Email
		}
		fmt.Fprintf(w, "Hello %s <%s>\n", me.ID, email)
	})

	http.HandleFunc("/members", func(w http.ResponseWriter, r *http.Request) {
		opts := []directory.RequestOption{
			directory.WithCookie(r.Header.Get("Cookie")),
			directory.WithHost(r.Host),
		}

		page, err := dirClient.ListMembers(nil, opts...)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		for _, m := range page.Items {
			fmt.Fprintf(w, "%s <%s> (%s)\n", m.Name, m.Email, m.Role)
		}
	})

	http.HandleFunc("/files/", func(w http.ResponseWriter, r *http.Request) {
		fileID := r.URL.Path[len("/files/"):]
		if fileID == "" {
			http.Error(w, "missing file ID", http.StatusBadRequest)
			return
		}

		content, err := mediaClient.Get(fileID)
		if err != nil {
			http.Error(w, err.Error(), http.StatusNotFound)
			return
		}
		defer content.Close()

		w.Header().Set("Content-Type", content.ContentType)
		io.Copy(w, content.Body)
	})

	log.Println("listening on :8080")
	log.Fatal(http.ListenAndServe(":8080", nil))
}
