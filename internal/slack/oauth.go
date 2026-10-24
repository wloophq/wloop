package slack

import (
	"context"
	"fmt"
	"net/url"
)

// OAuthArgs exchanges Code (with CodeVerifier and RedirectURI, PKCE) when set,
// else refreshes with RefreshToken. No client secret is ever sent.
type OAuthArgs struct {
	ClientID, Code, CodeVerifier, RedirectURI, RefreshToken string
}

// OAuthResponse is the user token, read under authed_user or at the top level.
// Its String and GoString never print the tokens.
type OAuthResponse struct {
	AccessToken, RefreshToken string
	ExpiresIn                 int
	Scope, User, Team         string
}

func (r OAuthResponse) String() string {
	return fmt.Sprintf("OAuthResponse{User: %q, Team: %q, Scope: %q, ExpiresIn: %d, tokens redacted}", r.User, r.Team, r.Scope, r.ExpiresIn)
}

func (r OAuthResponse) GoString() string { return r.String() }

// OAuthAccess calls oauth.v2.access without a bearer, so a TokenSource may call it while refreshing.
func (c *Client) OAuthAccess(ctx context.Context, a OAuthArgs) (OAuthResponse, error) {
	args := url.Values{"client_id": {a.ClientID}}
	if a.Code != "" {
		args.Set("grant_type", "authorization_code")
		args.Set("code", a.Code)
		setIf(args, "code_verifier", a.CodeVerifier)
		setIf(args, "redirect_uri", a.RedirectURI)
	} else {
		args.Set("grant_type", "refresh_token")
		args.Set("refresh_token", a.RefreshToken)
	}
	var out struct {
		oauthToken
		UserID     string      `json:"user_id"`
		AuthedUser *oauthToken `json:"authed_user"`
		Team       struct {
			ID string `json:"id"`
		} `json:"team"`
	}
	if _, err := c.call(ctx, "oauth.v2.access", noToken, args, &out); err != nil {
		return OAuthResponse{}, err
	}
	src := out.oauthToken
	if src.ID == "" {
		src.ID = out.UserID
	}
	if out.AuthedUser != nil && out.AuthedUser.AccessToken != "" {
		src = *out.AuthedUser
	}
	return OAuthResponse{AccessToken: src.AccessToken, RefreshToken: src.RefreshToken, ExpiresIn: src.ExpiresIn,
		Scope: src.Scope, User: src.ID, Team: out.Team.ID}, nil
}

type oauthToken struct {
	ID           string `json:"id"`
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	ExpiresIn    int    `json:"expires_in"`
	Scope        string `json:"scope"`
}
