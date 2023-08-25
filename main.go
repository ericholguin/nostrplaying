/*
MIT License

Copyright (c) 2023 Eric Holguin

Permission is hereby granted, free of charge, to any person obtaining a copy
of this software and associated documentation files (the "Software"), to deal
in the Software without restriction, including without limitation the rights
to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
copies of the Software, and to permit persons to whom the Software is
furnished to do so, subject to the following conditions:

The above copyright notice and this permission notice shall be included in all
copies or substantial portions of the Software.

THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE
SOFTWARE.
*/

package main

import (
	"bufio"
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/nbd-wtf/go-nostr"
	"github.com/zmb3/spotify/v2"
	spotifyauth "github.com/zmb3/spotify/v2/auth"
)

// redirectURI is the OAuth redirect URI for the application.
// You must register an application at Spotify's developer portal
// and enter this value.
const redirectURI = "http://localhost:8080/callback"

// NOSTR SECRET KEY
var nostrKey = os.Getenv("NOSTR_KEY")

// nowPlaying
var nowPlaying = ""

var html = `
<br/>
<a href="/player/play">Play</a><br/>
<a href="/player/pause">Pause</a><br/>
<a href="/player/next">Next track</a><br/>
<a href="/player/previous">Previous Track</a><br/>
<a href="/player/shuffle">Shuffle</a><br/>
<a href="/player/nowplaying">Now Playing</a><br/>

`

var (
	auth = spotifyauth.New(
		spotifyauth.WithRedirectURL(redirectURI),
		spotifyauth.WithScopes(spotifyauth.ScopeUserReadCurrentlyPlaying, spotifyauth.ScopeUserReadRecentlyPlayed, spotifyauth.ScopeUserReadPlaybackState, spotifyauth.ScopeUserModifyPlaybackState),
	)
	ch    = make(chan *spotify.Client)
	state = "nostrplaying"
)

func main() {

	var client *spotify.Client
	var playerState *spotify.PlayerState

	http.HandleFunc("/callback", completeAuth)

	http.HandleFunc("/player/", func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		action := strings.TrimPrefix(r.URL.Path, "/player/")
		fmt.Println("Got request for:", action)
		var err error
		switch action {
		case "play":
			err = client.Play(ctx)
		case "pause":
			err = client.Pause(ctx)
		case "next":
			err = client.Next(ctx)
		case "previous":
			err = client.Previous(ctx)
		case "shuffle":
			playerState.ShuffleState = !playerState.ShuffleState
			err = client.Shuffle(ctx, playerState.ShuffleState)
		case "nowplaying":
			getCurrentlyPlaying(client)
		}
		if err != nil {
			log.Print(err)
		}

		w.Header().Set("Content-Type", "text/html")
		fmt.Fprint(w, html)
	})

	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		log.Println("Got request for:", r.URL.String())
	})

	go func() {
		url := auth.AuthURL(state)
		fmt.Println("Please log in to Spotify by visiting the following page in your browser:", url)

		// wait for auth to complete
		client = <-ch

		// use the client to make calls that require authorization
		user, err := client.CurrentUser(context.Background())
		if err != nil {
			log.Fatal(err)
		}
		fmt.Println("You are logged in as:", user.ID)

		playerState, err = client.PlayerState(context.Background())
		if err != nil {
			log.Fatal(err)
		}
		fmt.Printf("Found your %s (%s)\n", playerState.Device.Type, playerState.Device.Name)

	}()

	http.ListenAndServe(":8080", nil)

}

func getCurrentlyPlaying(client *spotify.Client) {
	for {
		// Get the user's currently playing track
		currentlyPlaying, err := client.PlayerCurrentlyPlaying(context.Background())
		if err != nil {
			log.Fatalf("couldn't get currently playing track: %v", err)
		}
		var currentTrackName string
		var previousTrackName string
		if currentlyPlaying != nil && currentlyPlaying.Item != nil {
			currentTrackName = currentlyPlaying.Item.Name
		}

		file, err := os.OpenFile("previouslyPlayed.txt", os.O_RDWR, os.ModeAppend)
		if err != nil {
			log.Fatalf("couldn't open file: %v", err)
		}

		existingTracks := make(map[string]bool)
		scanner := bufio.NewScanner(file)
		for scanner.Scan() {
			line := scanner.Text()
			if len(line) > 0 {
				trackName := line[:strings.Index(line, "-")-1]
				existingTracks[trackName] = true
			}
		}
		if err := scanner.Err(); err != nil {
			log.Fatalf("couldn't read file: %v", err)
		}

		if _, ok := existingTracks[currentlyPlaying.Item.SimpleTrack.Name]; ok {
			fmt.Println("Track already played before:", currentlyPlaying.Item.Name)
		} else if previousTrackName == currentTrackName {
			fmt.Println("Track already played before:", currentlyPlaying.Item.Name)
		} else {
			track := currentlyPlaying.Item.SimpleTrack
			artists := make([]string, len(track.Artists))
			for j, artist := range track.Artists {
				artists[j] = artist.Name
			}
			line := fmt.Sprintf("%s - %s\n", track.Name, artists)
			if _, err := file.WriteString(line); err != nil {
				log.Fatalf("couldn't write to file: %v", err)
			}
			postNowPlaying(line, currentlyPlaying.Item.SimpleTrack.ExternalURLs["spotify"])
			previousTrackName = currentTrackName
		}

		// Wait 120 seconds before checking again
		time.Sleep(120 * time.Second)
	}
}

func postNowPlaying(name string, endpoint string) {
	sk := nostrKey
	pub, _ := nostr.GetPublicKey(sk)

	ev := nostr.Event{
		PubKey:    pub,
		CreatedAt: time.Now(),
		Kind:      30315,
		Tags:      []nostr.Tag{{"d", "music"}, {"r", endpoint}},
		Content:   name,
	}

	// calling Sign sets the event ID field and the event Sig field
	ev.Sign(sk)

	fmt.Printf("Content: %v\n", endpoint)

	// publish the event to these relays
	for _, url := range []string{"wss://relay.damus.io", "wss://nos.lol", "wss://nostr.wine", "wss://eden.nostr.land", "wss://relay.snort.social", "wss://blastr.f7z.xyz", "wss://relay.primal.net", "wss://relayable.org"} {
		relay, e := nostr.RelayConnect(context.Background(), url)
		if e != nil {
			fmt.Println(e)
			continue
		}
		status, err := relay.Publish(context.Background(), ev)
		if err != nil {
			fmt.Println(err)
			continue
		}
		fmt.Printf("published to %s: %v\n", url, status)
	}
}

func completeAuth(w http.ResponseWriter, r *http.Request) {
	tok, err := auth.Token(r.Context(), state, r)
	if err != nil {
		http.Error(w, "Couldn't get token", http.StatusForbidden)
		log.Fatal(err)
	}
	if st := r.FormValue("state"); st != state {
		http.NotFound(w, r)
		log.Fatalf("State mismatch: %s != %s\n", st, state)
	}
	// use the token to get an authenticated client
	client := spotify.New(auth.Client(r.Context(), tok))
	w.Header().Set("Content-Type", "text/html")
	fmt.Fprintf(w, "Login Completed!"+html)
	ch <- client
}
