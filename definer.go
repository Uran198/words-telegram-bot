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
//
//
// This file contains logic for extracting word definitions
package main

import (
	"fmt"
	"log"
	"net/http"
	"regexp"
	"strings"

	"golang.org/x/net/html"
)

// MaxMessageLength is a soft maximum on a single message length. In reality it
// is around 4096. Having stricter limit makes it simpler to add things like
// link to the source, or img without worrying about limits.
// Limit was chosen arbitrary. It is difficult to read long texts.
const MaxMessageLength = 1200

type Definer struct {
	usage *UsageFetcher
	http  *http.Client
}

// Define queries multiple sources for word meaning, translation or definition.
//
// Possible improvement is asynchronously perform queries, and return results
// to the user as we get responses. This might feel more responsive.
// Also, Caller might need to throttle number of messages send to the user.
// The limit right now is 20 messages per second, it may not be a problem.
// https://core.telegram.org/bots/faq#my-bot-is-hitting-limits-how-do-i-avoid-this
func (d *Definer) Define(word string, settings *Settings) (ds []string, err error) {
	ds, err = d.DefaultDefine(word, settings)
	log.Printf("DefaultDefine(%s, %v) err : %v", word, settings, err)
	if settings.InputLanguage == "Hungarian" {
		// Try fetching data from https://wikiszotar.hu
		if r, err := d.queryWikiSzotar(word); err != nil {
			log.Printf("queryWikiSzotar(%s) err : %v", word, err)
		} else {
			ds = append(ds, r...)
		}
	}
	if len(ds) > 0 {
		err = nil
	}
	return ds, err
}

// DefaultDefine fetches definitions relying on wiktionary and tatoeba data.
// For some languages it makes sense to use different resources that contain better definitions.
func (d *Definer) DefaultDefine(word string, settings *Settings) (ds []string, err error) {
	p := WikiParser{
		InputLanguage: settings.InputLanguage,
	}
	defs, err := FetchWikiDefinition(p, d.http, word)
	if err != nil {
		return nil, err
	}
	word = defs[0].Word

	ex, err := d.usage.FetchExamples(word, settings.InputLanguageISO639_3, settings.TranslationLanguages)
	if err != nil {
		ex = nil
		log.Printf("ERROR: FetchExamples(%s): %v", word, err)
		log.Printf("WARNING Did not find usage examples for %q", word)
	}
	msg := "*" + escapeMarkdown(word) + "*\n"
	for i, d := range defs {
		if i > 7 {
			msg += "\n"
			msg += fmt.Sprintf("_\\[truncated %d definitions\\]_", len(defs)-i)
			break
		}
		msg += "\n"
		msg += fmt.Sprintf(`%d\. \[*%s*\] %s`, i+1, strings.ToLower(d.SpeechPart), escapeMarkdown(d.Definition))
	}
	if len(ex) > 0 {
		msg += "\n\nUsage examples:"
		for i, e := range ex {
			msg += "\n\n"
			msg += fmt.Sprintf(`%d\. %s`, i+1, escapeMarkdown(e.Text))
			for _, t := range e.Translations {
				msg += "\n" + fmt.Sprintf(`  _%s_`, escapeMarkdown(t))
			}
		}
	} else {
		msg += escapeMarkdown("\n\nDidn't find usage examples.")
	}
	return []string{msg}, nil
}

func (d *Definer) queryWikiSzotar(word string) (defs []string, err error) {
	const urlPrefix = "https://wikiszotar.hu/"
	url := urlPrefix + "ertelmezo-szotar/" + word
	q, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return
	}
	r, err := d.http.Do(q)
	if err != nil {
		return
	}
	defer r.Body.Close()
	doc, err := html.Parse(r.Body)
	if err != nil {
		return
	}
	// Extract definition of the word.
	var extractContent func(*html.Node) (def string, img string)
	spaceRe := regexp.MustCompile(`\s+`)
	newlineRe := regexp.MustCompile(`\n\n+`)
	extractContent = func(n *html.Node) (def string, img string) {
		if n.Type == html.TextNode {
			return escapeMarkdown(n.Data), ""
		}
		for _, a := range n.Attr {
			if a.Key == "class" && strings.Contains(a.Val, "alert") {
				return
			}
		}
		if n.Data == "img" {
			for _, a := range n.Attr {
				if a.Key == "src" {
					return "", fmt.Sprintf("[img](%s)", urlPrefix+a.Val)
				}
			}
		}
		var b strings.Builder
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			if c.Data == "br" {
				b.WriteRune('\n')
			}
			s, i := extractContent(c)
			if i != "" {
				img = i
			}
			if spaceRe.ReplaceAllString(s, " ") != " " {
				if b.Len()+len(s) < MaxMessageLength {
					b.WriteString(s)
				} else {
					fmt.Fprintf(&b, "\n\n*%s*", escapeMarkdown("[truncated]"))
					return b.String(), img
				}
			}
		}
		if b.Len() == 0 {
			return
		}
		switch n.Data {
		case "b":
			return fmt.Sprintf("*%s*", b.String()), img
		case "h2":
			return fmt.Sprintf("*%s*\n\n", b.String()), img
		case "i":
			return fmt.Sprintf("_%s_", b.String()), img
		case "a", "span":
			return b.String(), img
		default:
			b.WriteRune('\n')
			b.WriteRune('\n')
			return b.String(), img
		}
	}
	content := func(n *html.Node) string {
		if s, img := extractContent(n); s != "" {
			// Img should be the first link so that telegram shows it.
			links := []string{}
			if img != "" {
				links = append(links, img)
			}
			links = append(links, fmt.Sprintf("[SOURCE](%s)", url))
			s += "\n\n" + strings.Join(links, " ")
			// No need to ever have more than 2 blank lines.
			return newlineRe.ReplaceAllString(s, "\n\n")
		}
		return ""
	}
	var f func(*html.Node) []string
	f = func(n *html.Node) []string {
		for _, a := range n.Attr {
			if a.Key == "class" && a.Val == "mw-parser-output" {
				var r []string
				hasSections := n.FirstChild != nil && n.FirstChild.Data == "div"
				if hasSections {
					for c := n.FirstChild; c != nil; c = c.NextSibling {
						// Try to break up each div into it's separate message.
						// Usefult for pages like https://wikiszotar.hu/ertelmezo-szotar/Fekete
						if c.Data == "div" {
							if s := content(c); s != "" {
								r = append(r, s)
							}
						}
					}
				} else {
					if s := content(n); s != "" {
						r = append(r, s)
					}
				}
				return r
			}
		}
		var r []string
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			r = append(r, f(c)...)
		}
		return r
	}
	defs = f(doc)
	if len(defs) == 0 {
		err = fmt.Errorf("No definitions found for %q", word)
	}
	return defs, err
}
