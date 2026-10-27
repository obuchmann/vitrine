package mcptools

import (
	"fmt"
	"time"

	"github.com/obuchmann/vitrine/internal/store"
)

// Instructions is the server's usage guide, shown to agents on connect. It
// names the token file, never the token. authFile is empty if the token comes
// from VITRINE_TOKEN.
func Instructions(goos, baseURL, authFile string, ttl time.Duration) string {
	return fmt.Sprintf(`vitrine shows HTML prototypes to people and tells you what they do in them.

Rules:
- The repository is the source. vitrine keeps a prototype and its events
  until it has had no activity for %s; you then get "unknown_prototype" and
  publish it again from the repository.
- Write one self-contained HTML file (CSS, JS and images inline) and publish
  it with publish_prototype {path}. vitrine reads the file, so the HTML does
  not pass through your context.
- Names are <repo>/<name>. repo defaults to the git repository that contains
  the file, name to the file's folder. The page is at %s/p/<repo>/<name>/ and opens in
  the user's browser on the first publish.
- In the prototype, report what matters: hub.emit("choice", {variant: "B"}).
  vitrine injects hub.js. <html data-track="view,click"> adds automatic events.
- Listen with wait_for_events, or in the background so you stay free:
    %s
  The command ends when an event arrives and prints it as JSON.
- Events stay unread until you acknowledge them: pass the cursor of the
  previous result as ack. It acknowledges only events you received; those a
  types filter left out stay unread. In a new session, call list_prototypes,
  then wait_for_events with ack=0: you get everything not yet acknowledged.
- Write decisions you read from events into the repository (e.g. decisions.md).`,
		store.FormatDuration(ttl), baseURL, WaitCommand(goos, baseURL, authFile))
}

// WaitCommand is the background wait for the platform's shell. The token
// file holds the headers, so the token is not on the command line. In
// PowerShell, curl is an alias for Invoke-WebRequest and @ starts splatting,
// hence curl.exe and the quotes.
func WaitCommand(goos, baseURL, authFile string) string {
	curl, headers := "curl", fmt.Sprintf(`-H "@%s"`, authFile)
	if goos == "windows" {
		curl = "curl.exe"
	}
	if authFile == "" {
		tok := "$VITRINE_TOKEN"
		if goos == "windows" {
			tok = "$env:VITRINE_TOKEN"
		}
		headers = fmt.Sprintf(`-H "Authorization: Bearer %s" -H "X-Vitrine: 1"`, tok)
	}
	return fmt.Sprintf(`%s -s %s '%s/api/events/next?name=<repo>/<name>&types=choice,comment&ack=<cursor>'`, curl, headers, baseURL)
}
