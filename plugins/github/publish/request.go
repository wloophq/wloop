package publish

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"slices"
	"strings"

	src "github.com/wloophq/wloop/pkg/plugin/source"
	"github.com/wloophq/wloop/plugins/github/api"
	"github.com/wloophq/wloop/plugins/github/pr"
)

// ReviewRequest is a request_review draft's payload, which
// drafts/request_review/schema.json shapes.
type ReviewRequest struct {
	Reviewers []string `json:"reviewers"`
}

// DecodeReviewRequest is a request_review draft's payload, refused when it
// does not fit the kind's schema.
func DecodeReviewRequest(raw json.RawMessage) (ReviewRequest, error) {
	var r ReviewRequest
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&r); err != nil {
		return ReviewRequest{}, fmt.Errorf("a review request's payload: %w", err)
	}
	if dec.Decode(new(json.RawMessage)) != io.EOF {
		return ReviewRequest{}, errors.New("a review request's payload: text after the object")
	}
	if len(r.Reviewers) == 0 {
		return ReviewRequest{}, errors.New("a review request names its reviewers")
	}
	for _, login := range r.Reviewers {
		if login == "" || strings.ContainsAny(login, "@/ \t\n") {
			return ReviewRequest{}, fmt.Errorf("reviewer %q is not a GitHub login", login)
		}
	}
	return r, nil
}

func prepareRequest(d src.Draft) (pr.Ref, ReviewRequest, error) {
	ref, err := selfRef(d)
	if err != nil {
		return pr.Ref{}, ReviewRequest{}, err
	}
	r, err := DecodeReviewRequest(d.Payload)
	return ref, r, err
}

type user struct {
	Login string `json:"login"`
}

func requestedPath(ref pr.Ref) string { return pullPath(ref) + "/requested_reviewers" }

// publishRequest asks d's reviewers for a review of its pull request.
func (p *Publisher) publishRequest(ctx context.Context, d src.Draft) (src.Evidence, error) {
	ref, r, err := prepareRequest(d)
	if err != nil {
		return src.Evidence{}, err
	}
	payload, err := json.Marshal(r)
	if err != nil {
		return src.Evidence{}, err
	}
	if _, err := p.GH.REST(ctx, api.Request{Method: http.MethodPost, Path: requestedPath(ref), Body: payload, Mutation: true}); err != nil {
		if written(err) {
			return src.Evidence{}, fmt.Errorf("%w on %s: %w", src.ErrMayHavePosted, ref, err)
		}
		return src.Evidence{}, fmt.Errorf("requesting reviews of %s: %w", ref, err)
	}
	return src.Evidence{Ref: strings.Join(r.Reviewers, ","), Permalink: ref.URL(), At: p.Now()}, nil
}

// verifyRequest says whether every reviewer d asks is a requested reviewer
// of its pull request now.
func (p *Publisher) verifyRequest(ctx context.Context, d src.Draft) (src.Evidence, bool, error) {
	ref, r, err := prepareRequest(d)
	if err != nil {
		return src.Evidence{}, false, err
	}
	resp, err := p.GH.REST(ctx, api.Request{Path: requestedPath(ref)})
	if err != nil {
		return src.Evidence{}, false, fmt.Errorf("reading the requested reviewers of %s: %w", ref, err)
	}
	var got struct {
		Users []user `json:"users"`
	}
	if err := json.Unmarshal(resp.Body, &got); err != nil {
		return src.Evidence{}, false, fmt.Errorf("reading the requested reviewers of %s: %w", ref, err)
	}
	for _, want := range r.Reviewers {
		if !slices.ContainsFunc(got.Users, func(u user) bool { return strings.EqualFold(u.Login, want) }) {
			return src.Evidence{}, false, nil
		}
	}
	return src.Evidence{Ref: strings.Join(r.Reviewers, ","), Permalink: ref.URL(), At: p.Now()}, true, nil
}
