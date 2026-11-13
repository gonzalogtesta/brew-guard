package dates

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const (
	// APIBase is the only host brew-guard talks to for dates.
	APIBase  = "https://api.github.com"
	maxBody  = 8 << 20
	perPage  = 100
	maxPages = 10
)

// GitHub reads file history from the GitHub REST API. Token is optional and
// raises the rate limit from 60 to 5000 requests per hour; it is only ever
// sent to BaseURL's host.
type GitHub struct {
	BaseURL   string
	Token     string
	UserAgent string
	Client    *http.Client
}

// NewGitHub returns a client for api.github.com.
func NewGitHub(token, userAgent string) *GitHub {
	return &GitHub{BaseURL: APIBase, Token: token, UserAgent: userAgent}
}

func (g *GitHub) client() *http.Client {
	if g.Client != nil {
		return g.Client
	}
	return &http.Client{
		Timeout: 20 * time.Second,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			first := via[0].URL
			if req.URL.Host != first.Host || req.URL.Scheme != first.Scheme {
				return errors.New("refusing redirect to another host or scheme")
			}
			if len(via) > 3 {
				return errors.New("too many redirects")
			}
			return nil
		},
	}
}

type apiCommit struct {
	SHA    string `json:"sha"`
	Commit struct {
		Message   string `json:"message"`
		Committer struct {
			Date time.Time `json:"date"`
		} `json:"committer"`
	} `json:"commit"`
}

// History returns up to maxPages*perPage commits touching path in repo
// ("owner/name"), newest first. If a page after the first one fails (GitHub
// answers 422 "taking too long" for formulae with a very long history, like
// ffmpeg), the commits already fetched are returned. A shorter history can
// only make a version look newer than it is, or leave it unfound, and both
// end in a block, so this stays on the safe side.
func (g *GitHub) History(ctx context.Context, repo, path string) ([]Commit, error) {
	var out []Commit
	for page := 1; page <= maxPages; page++ {
		q := url.Values{"path": {path}, "per_page": {fmt.Sprint(perPage)}, "page": {fmt.Sprint(page)}}
		u := fmt.Sprintf("%s/repos/%s/commits?%s", strings.TrimRight(g.BaseURL, "/"), repo, q.Encode())
		batch, err := g.fetch(ctx, u)
		if err != nil {
			if page > 1 && ctx.Err() == nil {
				break
			}
			return nil, err
		}
		for _, c := range batch {
			msg, _, _ := strings.Cut(c.Commit.Message, "\n")
			out = append(out, Commit{SHA: c.SHA, Time: c.Commit.Committer.Date, Message: msg})
		}
		if len(batch) < perPage {
			break
		}
	}
	return out, nil
}

func (g *GitHub) fetch(ctx context.Context, u string) ([]apiCommit, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	if g.UserAgent != "" {
		req.Header.Set("User-Agent", g.UserAgent)
	}
	if g.Token != "" {
		req.Header.Set("Authorization", "Bearer "+g.Token)
	}
	resp, err := g.client().Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		if resp.StatusCode == http.StatusForbidden && resp.Header.Get("X-RateLimit-Remaining") == "0" {
			return nil, errors.New("GitHub rate limit reached; set GITHUB_TOKEN or use the cache")
		}
		return nil, fmt.Errorf("GitHub API: %s", resp.Status)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBody+1))
	if err != nil {
		return nil, err
	}
	if len(body) > maxBody {
		return nil, errors.New("GitHub response too large")
	}
	var commits []apiCommit
	if err := json.Unmarshal(body, &commits); err != nil {
		return nil, err
	}
	return commits, nil
}
