package main

import (
	"encoding/json"
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"net/http"
	"net/url"
	"os"
	"strings"

	"github.com/utilitywarehouse/go-operational/op"
)

var (
	clientID     = os.Getenv("CLIENT_ID")
	clientSecret = os.Getenv("CLIENT_SECRET")
	callbackURL  = os.Getenv("CALLBACK_URL")
	expectedHostedDomain = os.Getenv("HOSTED_DOMAIN")
)

const oauthURL = "https://accounts.google.com/o/oauth2/auth?redirect_uri=%s&response_type=code&client_id=%s&scope=openid+email+profile&approval_prompt=force&access_type=offline"
const tokenURL = "https://www.googleapis.com/oauth2/v3/token"
const userInfoURL = "https://www.googleapis.com/oauth2/v1/userinfo"
const idpIssuerURL = "https://accounts.google.com"
const kubectlCMDTemplate = "# Run the following command to configure a kubernetes user for use with `kubectl`\n# ATTENTION iTerm2 users, make sure to run the following in a new terminal/tab\nkubectl config set-credentials %s \\\n--auth-provider=oidc \\\n--auth-provider-arg=client-id=%s \\\n--auth-provider-arg=client-secret=%s \\\n--auth-provider-arg=id-token=%s \\\n--auth-provider-arg=idp-issuer-url=%s \\\n--auth-provider-arg=refresh-token=%s"

type GoogleConfig struct {
	ClientID     string `json:"client_id"`
	ClientSecret string `json:"client_secret"`
}

type UserInfo struct {
	Email string `json:"email"`
}

type HostedDomain struct {
	HostedDomain string `json:"hd"`
}

type TokenResponse struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	IdToken      string `json:"id_token"`
}

// Get the id_token and refresh_token from google
func getTokens(clientID, clientSecret, code string) (*TokenResponse, error) {
	val := url.Values{}
	val.Add("grant_type", "authorization_code")
	val.Add("redirect_uri", callbackURL)
	val.Add("client_id", clientID)
	val.Add("client_secret", clientSecret)
	val.Add("code", code)

	resp, err := http.PostForm(tokenURL, val)
	if err != nil {
		return nil, err
	}
	defer func() {
		io.Copy(ioutil.Discard, resp.Body)
		resp.Body.Close()
	}()
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("Got: %d calling %s", resp.StatusCode, tokenURL)
	}
	if err != nil {
		return nil, err
	}
	tr := &TokenResponse{}
	err = json.NewDecoder(resp.Body).Decode(tr)
	if err != nil {
		return nil, err
	}
	return tr, nil
}

func getUserEmail(accessToken string) (string, error) {
	uri, _ := url.Parse(userInfoURL)
	q := uri.Query()
	q.Set("alt", "json")
	q.Set("access_token", accessToken)
	uri.RawQuery = q.Encode()
	resp, err := http.Get(uri.String())
	if err != nil {
		return "", err
	}
	defer func() {
		io.Copy(ioutil.Discard, resp.Body)
		resp.Body.Close()
	}()
	if resp.StatusCode != 200 {
		return "", fmt.Errorf("Got: %d calling %s", resp.StatusCode, tokenURL)
	}
	if err != nil {
		return "", err
	}
	ui := &UserInfo{}
	err = json.NewDecoder(resp.Body).Decode(ui)
	if err != nil {
		return "", err
	}
	return ui.Email, nil
}

func getHostedDomain(accessToken string) (string, error) {
	uri, _ := url.Parse(userInfoURL)
	q := uri.Query()
	q.Set("alt", "json")
	q.Set("access_token", accessToken)
	uri.RawQuery = q.Encode()
	resp, err := http.Get(uri.String())
	if err != nil {
		return "", err
	}
	defer func() {
		io.Copy(ioutil.Discard, resp.Body)
		resp.Body.Close()
	}()
	if resp.StatusCode != 200 {
		return "", fmt.Errorf("Got: %d calling %s", resp.StatusCode, tokenURL)
	}
	if err != nil {
		return "", err
	}
	hd := &HostedDomain{}
	err = json.NewDecoder(resp.Body).Decode(hd)
	if err != nil {
		return "", err
	}
	return hd.HostedDomain, nil
}

func googleRedirect() http.Handler {
	redirectURL := fmt.Sprintf(oauthURL, callbackURL, clientID)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, redirectURL, http.StatusFound)
	})
}

func googleCallback() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		code := r.URL.Query().Get("code")
		tokResponse, err := getTokens(clientID, clientSecret, code)

		if err != nil {
			log.Printf("Error getting tokens: %s\n", err)
			w.WriteHeader(http.StatusInternalServerError)
		}

		email, err := getUserEmail(tokResponse.AccessToken)
		if err != nil {
			log.Printf("Error getting user email: %s\n", err)
			w.WriteHeader(http.StatusInternalServerError)
		}

		hostedDomain, err := getHostedDomain(tokResponse.AccessToken)
		if err != nil {
			log.Printf("Error getting user hosted domain: %s\n", err)
			w.WriteHeader(http.StatusInternalServerError)
		}

		if ! strings.EqualFold(hostedDomain, expectedHostedDomain) {
			log.Printf("Error hosted domain does not match (was %s instead of %s)\n", hostedDomain, expectedHostedDomain)
			http.Error(w, "Forbidden", 403)
			return
		}

		kubectlCMD := fmt.Sprintf(kubectlCMDTemplate, email, clientID, clientSecret, tokResponse.IdToken, idpIssuerURL, tokResponse.RefreshToken)

		w.WriteHeader(http.StatusOK)
		_, err = w.Write([]byte(kubectlCMD))
		if err != nil {
			log.Println("failed to write about response")
			w.WriteHeader(http.StatusInternalServerError)
		}
	})
}

func main() {
	m := http.NewServeMux()

	m.Handle("/", googleRedirect())
	m.Handle("/callback", googleCallback())

	http.Handle("/__/", op.NewHandler(
		op.NewStatus("Kubernetes config builder", "Constructs kube config for the user to allow access to the api server.").
			AddOwner("Infrastructure", "#infra").
			ReadyUseHealthCheck(),
	),
	)

	http.Handle("/", m)
	log.Println("Listening on :8080")
	http.ListenAndServe(":8080", nil)
}
