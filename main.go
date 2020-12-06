// Copyright 2020 Google LLC
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     https://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.
package main

import (
	"context"
	"flag"
	"log"
	"math/rand"
	"net/http"
	"time"

	_ "github.com/mattn/go-sqlite3"
)

// TODO: Start and PollAndProcess should be part of the commander
func Start(ctx context.Context, opts *CommanderOptions) error {
	// TODO: Move telegram building into NewCommander, NewCommander will accept
	// only http.Client
	t := &Telegram{hc: http.Client{}}
	c, err := NewCommander(t, opts)
	if err != nil {
		return err
	}
	if opts.push {
		return c.StartPush(opts)
	} else {
		return c.StartPoll()
	}
}

func main() {
	log.SetFlags(log.Flags() | log.Lshortfile)

	db := flag.String("db_path", "./db.sql", "Path to the persistent sqlite3 database.")

	push := flag.Bool("push", false, "If true will register webhook, otherwise will rely on polling to get updates.")
	ip := flag.String("ip", "", "IP address of the server. Needed only if push is set to true.")
	port := flag.Int("port", 8443, "Port of which webhook should listen. Needed only if push is set to true.")
	cert := flag.String("cert_path", "webhook.crt", "TLS certificate. Needed only if push is set to true.")
	key := flag.String("key_path", "webhook.key", "Private key for TLS. Needed only if push is set to true.")

	flag.Parse()
	log.Printf("db_path: %q", *db)

	rand.Seed(time.Now().UnixNano())
	ctx := context.Background()
	opts := &CommanderOptions{
		dbPath:     *db,
		port:       *port,
		certPath:   *cert,
		keyPath:    *key,
		ip:         *ip,
		push:       *push,
		againDelay: 20 * time.Second,
		stages: []time.Duration{
			20 * time.Second,
			1 * time.Hour * 23,
			2 * time.Hour * 23,
			3 * time.Hour * 23,
			5 * time.Hour * 23,
			8 * time.Hour * 24,
			13 * time.Hour * 24,
			21 * time.Hour * 24,
			34 * time.Hour * 24,
			55 * time.Hour * 24,
			89 * time.Hour * 24,
			144 * time.Hour * 24,
			233 * time.Hour * 24,
			377 * time.Hour * 24,
		},
		wordsCacheSize: 100,
	}
	if err := Start(ctx, opts); err != nil {
		log.Fatal(err)
	}
}
