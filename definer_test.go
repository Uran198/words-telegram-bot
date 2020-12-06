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
	"database/sql"
	"io/ioutil"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	_ "github.com/mattn/go-sqlite3"
)

// TODO: Write local test that doesn't do HTTP requests.
func TestDefinerNonhermetic(t *testing.T) {
	dir, err := ioutil.TempDir("", "test")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	dbPath := filepath.Join(dir, "tmpdb")

	// Initiate db with some usage examples.
	db, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(usageSQL); err != nil {
		t.Fatal(err)
	}
	uf, err := NewUsageFetcher(dbPath)
	if err != nil {
		t.Fatal(err)
	}

	definer := &Definer{
		usage: uf,
		http:  &http.Client{},
	}
	d, err := definer.Define("fekete", DefaultSettings())
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("Success:\n%s\nlength: %d",
		strings.Join(d, "\n"+strings.Repeat("-", 70)+"\n"),
		len(d),
	)
	if len(d) != 3 {
		t.Errorf("got %d definitions, want 3", len(d))
	}
}
