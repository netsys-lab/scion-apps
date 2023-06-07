// Copyright 2018 ETH Zurich
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//   http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package main

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"flag"
	"fmt"
	"log"
	"math/big"
	"net/http"
	"os"
	"time"

	"github.com/gorilla/handlers"
	"github.com/netsec-ethz/scion-apps/pkg/shttp3"
	"github.com/quic-go/quic-go/http3"
)

func main() {
	certFile := flag.String("cert", "", "Path to TLS server certificate ")
	keyFile := flag.String("key", "", "Path to TLS server key ")
	flag.Parse()

	m := http.NewServeMux()

	// handler that responds with a friendly greeting
	m.HandleFunc("/hello", func(w http.ResponseWriter, r *http.Request) {
		// Status 200 OK will be set implicitly
		w.Header().Set("Content-Type", "text/plain")
		_, _ = w.Write([]byte(`Oh, hello!`))
	})

	// handler that responds with an image file
	m.HandleFunc("/image", func(w http.ResponseWriter, r *http.Request) {
		// serve the sample JPG file
		// Status 200 OK will be set implicitly
		// Content-Length will be inferred by server
		// Content-Type will be detected by server
		http.ServeFile(w, r, "dog.jpg")
	})

	// GET handler that responds with some json data
	m.HandleFunc("/json", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			data := struct {
				Time    string
				Agent   string
				Proto   string
				Message string
			}{
				Time:    time.Now().Format("2006.01.02 15:04:05"),
				Agent:   r.UserAgent(),
				Proto:   r.Proto,
				Message: "success",
			}
			resp, _ := json.Marshal(data)
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, string(resp))
		} else {
			http.Error(w, "wrong method: "+r.Method, http.StatusForbidden)
		}
	})

	// POST handler that responds by parsing form values and returns them as string
	m.HandleFunc("/form", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			if err := r.ParseForm(); err != nil {
				http.Error(w, "invalid form data", http.StatusBadRequest)
				return
			}
			w.Header().Set("Content-Type", "text/plain")
			fmt.Fprint(w, "received following data:\n")
			for s := range r.PostForm {
				fmt.Fprint(w, s, "=", r.PostFormValue(s), "\n")
			}
		} else {
			http.Error(w, "wrong method: "+r.Method, http.StatusForbidden)
		}
	})

	handler := handlers.LoggingHandler(os.Stdout, m)
	if *certFile != "" && *keyFile != "" {
		go func() { log.Fatal(shttp3.ListenAndServe(":443", *certFile, *keyFile, handler)) }()
	}
	// if no TLS cert specified
	s := &shttp3.Server{
		Server: &http3.Server{
			Addr:      ":443",
			Handler:   handler,
			TLSConfig: generateTLSConfig(),
		},
	}

	log.Fatal(s.ListenAndServe())
}

// Setup a bare-bones TLS config for the server
// from github.com/quic-go/quic-go/example/echo/echo.go
func generateTLSConfig() *tls.Config {
	key, err := rsa.GenerateKey(rand.Reader, 1024)
	if err != nil {
		panic(err)
	}
	//var uris []*url.URL
	//uris = append(uris, &url.URL{Host: "19-ffaa:1:1067,127.0.0.1"})
	var dnsnames []string
	dnsnames = append(dnsnames, "19-ffaa:1:1067,127.0.0.1")

	template := x509.Certificate{SerialNumber: big.NewInt(1),
		NotAfter: time.Now().Add(24 * time.Hour),
		DNSNames: dnsnames,

		//URIs:     uris,
	}
	// 19-ffaa:1:1067,127.0.0.1
	certDER, err := x509.CreateCertificate(rand.Reader, &template, &template, &key.PublicKey, key)
	if err != nil {
		panic(err)
	}
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)})
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certDER})

	tlsCert, err := tls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		panic(err)
	}
	return &tls.Config{
		Certificates: []tls.Certificate{tlsCert},
		NextProtos:   []string{"http3-example"},
	}
}
