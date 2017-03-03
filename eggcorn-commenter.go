// This program takes comment emails from https://github.com/horgh/eggcorn and
// generates HTML from them.
//
// It finds the emails in a specified Maildir. Each message contains one comment
// in a JSON payload in the body of the mail. It parses the messages and the
// JSON in each, and outputs HTML files based on the page name found in each
// comment.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"html/template"
	"io/ioutil"
	"net"
	"net/mail"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Args holds command line arguments.
type Args struct {
	Maildir string
	HTMLDir string
}

// Comment holds information about a single comment.
type Comment struct {
	Name      string
	Email     string
	Text      string
	URL       string
	IP        net.IP
	UserAgent string
	Time      time.Time
	ID        string
}

// ByTime implements sort.Interface for []*Comment based on the Time field.
type ByTime []*Comment

func (t ByTime) Len() int      { return len(t) }
func (t ByTime) Swap(i, j int) { t[i], t[j] = t[j], t[i] }
func (t ByTime) Less(i, j int) bool {
	if !t[i].Time.Equal(t[j].Time) {
		return t[i].Time.Before(t[j].Time)
	}
	return t[i].ID < t[j].ID
}

func main() {
	args, err := getArgs()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		flag.PrintDefaults()
		os.Exit(1)
	}

	comments, err := parseMails(args.Maildir)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

	for rawURL, pageComments := range comments {
		sort.Sort(ByTime(pageComments))
		err := writeHTML(args.HTMLDir, rawURL, pageComments)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Unable to write HTML for page: %s: %s\n", rawURL,
				err)
			os.Exit(1)
		}
	}
}

func getArgs() (*Args, error) {
	maildir := flag.String("maildir", "", "Path to Maildir containing comment emails.")
	htmlDir := flag.String("html-dir", "", "Path to directory to write HTML files.")

	flag.Parse()

	if len(*maildir) == 0 {
		return nil, fmt.Errorf("you must provide a maildir")
	}

	if len(*htmlDir) == 0 {
		return nil, fmt.Errorf("you must provide an HTML directory")
	}

	return &Args{
		Maildir: *maildir,
		HTMLDir: *htmlDir,
	}, nil
}

// I recursively descend the Maildir and process all files as if they are mails.
//
// Yes, we should only really need to look in the cur (and maybe new)
// directories.
//
// Return Comments keyed by the page URL that the comment is on.
func parseMails(maildir string) (map[string][]*Comment, error) {
	dh, err := os.Open(maildir)
	if err != nil {
		return nil, err
	}

	names, err := dh.Readdirnames(0)
	if err != nil {
		_ = dh.Close()
		return nil, fmt.Errorf("error reading dir names: %s", err)
	}

	if err := dh.Close(); err != nil {
		return nil, fmt.Errorf("error closing: %s: %s", maildir, err)
	}

	comments := map[string][]*Comment{}

	for _, filename := range names {
		if filename == "." || filename == ".." {
			continue
		}

		path := filepath.Join(maildir, filename)

		fi, err := os.Stat(path)
		if err != nil {
			return nil, fmt.Errorf("stat: %s: %s", path, err)
		}

		if fi.IsDir() {
			dirComments, err := parseMails(path)
			if err != nil {
				return nil, err
			}

			for k, v := range dirComments {
				_, exists := comments[k]
				if !exists {
					comments[k] = []*Comment{}
				}
				comments[k] = append(comments[k], v...)
			}

			continue
		}

		comment, err := parseMail(path)
		if err != nil {
			return nil, err
		}

		_, exists := comments[comment.URL]
		if !exists {
			comments[comment.URL] = []*Comment{}
		}
		comments[comment.URL] = append(comments[comment.URL], comment)
	}

	return comments, nil
}

func parseMail(path string) (*Comment, error) {
	fh, err := os.Open(path)
	if err != nil {
		return nil, err
	}

	defer func() {
		err := fh.Close()
		if err != nil {
			fmt.Fprintf(os.Stderr, "close error: %s: %s\n", path, err)
		}
	}()

	message, err := mail.ReadMessage(fh)
	if err != nil {
		return nil, fmt.Errorf("unable to parse mail: %s: %s", path, err)
	}

	body, err := ioutil.ReadAll(message.Body)
	if err != nil {
		return nil, fmt.Errorf("unable to read body: %s", err)
	}

	type messageAttributesJSON struct {
		Name      map[string]string
		Email     map[string]string
		Text      map[string]string
		URL       map[string]string
		IP        map[string]string
		UserAgent map[string]string
		Time      map[string]string
		ID        map[string]string
	}

	type commentJSON struct {
		MessageAttributes messageAttributesJSON
	}

	cj := commentJSON{}

	if err := json.Unmarshal(body, &cj); err != nil {
		return nil, fmt.Errorf("unable to decode JSON: %s", err)
	}

	ip := net.ParseIP(cj.MessageAttributes.IP["Value"])
	if ip == nil {
		return nil, fmt.Errorf("invalid IP found in message: %s",
			cj.MessageAttributes.IP["Value"])
	}

	unixtimeMS, err := strconv.Atoi(cj.MessageAttributes.Time["Value"])
	if err != nil {
		return nil, fmt.Errorf("invalid unixtime: %s: %s",
			cj.MessageAttributes.Time["Value"], err)
	}
	unixtimeS := int64(unixtimeMS / 1000)

	t := time.Unix(unixtimeS, 0)

	// All fields should have been trimmed of whitespace and checked to not be
	// blank prior to being encoded as JSON. However, let's still check that all
	// fields are here. For one thing this will help recognize if there is a
	// mistake in decoding.
	c := &Comment{
		Name:      cj.MessageAttributes.Name["Value"],
		Email:     cj.MessageAttributes.Email["Value"],
		Text:      cj.MessageAttributes.Text["Value"],
		URL:       cj.MessageAttributes.URL["Value"],
		IP:        ip,
		UserAgent: cj.MessageAttributes.UserAgent["Value"],
		Time:      t,
		ID:        cj.MessageAttributes.ID["Value"],
	}

	if err := c.isValid(); err != nil {
		return nil, fmt.Errorf("invalid comment: %s", err)
	}

	return c, nil
}

func (c Comment) isValid() error {
	if c.Name == "" {
		return fmt.Errorf("missing name")
	}
	if c.Email == "" {
		return fmt.Errorf("missing email")
	}
	if c.Text == "" {
		return fmt.Errorf("missing text")
	}
	if c.URL == "" {
		return fmt.Errorf("missing URL")
	}
	if c.IP == nil {
		return fmt.Errorf("missing IP")
	}
	if c.UserAgent == "" {
		return fmt.Errorf("missing UserAgent")
	}
	if c.Time.IsZero() {
		return fmt.Errorf("missing time")
	}
	if c.ID == "" {
		return fmt.Errorf("missing ID")
	}
	return nil
}

func writeHTML(htmlDir, rawURL string, comments []*Comment) error {
	// We base the file we write's name on the URL's path. Parse the URL and take
	// its path.

	u, err := url.Parse(rawURL)
	if err != nil {
		return fmt.Errorf("invalid URL: %s: %s", rawURL, err)
	}

	if len(u.Path) == 0 || len(u.Path) == 1 {
		return fmt.Errorf("no path found in URL: %s", rawURL)
	}

	// u.Path should begin with /. Strip that to make the filename.
	filename := u.Path[1:]

	// There should be no more / characters.
	if idx := strings.Index(filename, "/"); idx != -1 {
		return fmt.Errorf("unexpected path, too many '/' characters: %s", rawURL)
	}

	// Build the path to the file we're going to write.
	path := filepath.Join(htmlDir, filename)

	htmlFragment := `
<h2>Comments</h2>
{{range .Comments}}
<div class="comment">
	<div class="comment-name">{{.Name}}</div>
	<time>{{.Time}}</time>
	<div class="comment-text">
		{{.Text}}
	</div>
</div>
{{end}}
`

	t, err := template.New("comments").Parse(htmlFragment)
	if err != nil {
		return fmt.Errorf("unable to parse template: %s", err)
	}

	data := struct {
		Comments []*Comment
	}{
		Comments: comments,
	}

	fh, err := os.Create(path)
	if err != nil {
		return err
	}

	if err := t.Execute(fh, data); err != nil {
		_ = fh.Close()
		return fmt.Errorf("unable to execute template: %s", err)
	}

	if err := fh.Close(); err != nil {
		return fmt.Errorf("problem closing file: %s", err)
	}

	fmt.Printf("Wrote %s (%d comments)\n", path, len(comments))

	return nil
}
