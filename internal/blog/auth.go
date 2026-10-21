package blog

import (
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"golang.org/x/crypto/bcrypt"
)

const authCookieName = "go_blog_session"
const registrationCookieName = "go_blog_registration"

type sessionClaims struct {
	UserID     int64  `json:"uid"`
	Expiry     int64  `json:"exp"`
	Nonce      string `json:"nonce"`
	Credential string `json:"credential"`
}

type Auth struct {
	secret []byte
	secure bool
}

func NewAuth(secret string, secure bool) (*Auth, bool, error) {
	ephemeral := false
	if len(secret) < 32 {
		value, err := randomToken(32)
		if err != nil {
			return nil, false, err
		}
		secret = value
		ephemeral = true
	}
	return &Auth{secret: []byte(secret), secure: secure}, ephemeral, nil
}

func (a *Auth) PasswordMatches(hash, password string) bool {
	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(password)) == nil
}

func (a *Auth) SetSession(w http.ResponseWriter, userID int64, passwordHash string) error {
	nonce, err := randomToken(18)
	if err != nil {
		return err
	}
	claims := sessionClaims{UserID: userID, Expiry: time.Now().Add(12 * time.Hour).Unix(), Nonce: nonce}
	claims.Credential = a.credential(userID, passwordHash)
	payload, err := json.Marshal(claims)
	if err != nil {
		return err
	}
	value := base64.RawURLEncoding.EncodeToString(payload) + "." + base64.RawURLEncoding.EncodeToString(a.sign(payload))
	http.SetCookie(w, &http.Cookie{
		Name: authCookieName, Value: value, Path: "/", MaxAge: 12 * 60 * 60,
		Expires: time.Now().Add(12 * time.Hour), HttpOnly: true, Secure: a.secure, SameSite: http.SameSiteLaxMode,
	})
	return nil
}

func (a *Auth) credential(userID int64, passwordHash string) string {
	return base64.RawURLEncoding.EncodeToString(a.sign([]byte("credential:" + strconv.FormatInt(userID, 10) + ":" + passwordHash)))
}

func (a *Auth) ValidSessionUser(claims sessionClaims, user User) bool {
	return claims.UserID == user.ID && subtle.ConstantTimeCompare([]byte(claims.Credential), []byte(a.credential(user.ID, user.PasswordHash))) == 1
}

func validatePassword(password string) error {
	if len([]rune(password)) < 10 {
		return errors.New("密码至少需要 10 个字符")
	}
	if len(password) > 72 {
		return errors.New("密码不能超过 72 字节（英文字母通常占 1 字节，汉字通常占 3 字节）")
	}
	return nil
}

func (a *Auth) ClearSession(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name: authCookieName, Value: "", Path: "/", MaxAge: -1,
		Expires: time.Unix(1, 0), HttpOnly: true, Secure: a.secure, SameSite: http.SameSiteLaxMode,
	})
}

func (a *Auth) SetRegistrationToken(w http.ResponseWriter) (string, error) {
	token, err := randomToken(24)
	if err != nil {
		return "", err
	}
	http.SetCookie(w, &http.Cookie{
		Name: registrationCookieName, Value: token, Path: "/register", MaxAge: 15 * 60,
		Expires: time.Now().Add(15 * time.Minute), HttpOnly: true, Secure: a.secure, SameSite: http.SameSiteStrictMode,
	})
	return token, nil
}

func (a *Auth) ValidRegistrationToken(r *http.Request, value string) bool {
	cookie, err := r.Cookie(registrationCookieName)
	if err != nil || value == "" {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(cookie.Value), []byte(value)) == 1
}

func (a *Auth) ClearRegistrationToken(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name: registrationCookieName, Value: "", Path: "/register", MaxAge: -1,
		Expires: time.Unix(1, 0), HttpOnly: true, Secure: a.secure, SameSite: http.SameSiteStrictMode,
	})
}

func (a *Auth) Claims(r *http.Request) (sessionClaims, error) {
	cookie, err := r.Cookie(authCookieName)
	if err != nil {
		return sessionClaims{}, err
	}
	parts := strings.Split(cookie.Value, ".")
	if len(parts) != 2 {
		return sessionClaims{}, errors.New("invalid session")
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return sessionClaims{}, err
	}
	signature, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil || subtle.ConstantTimeCompare(signature, a.sign(payload)) != 1 {
		return sessionClaims{}, errors.New("invalid signature")
	}
	var claims sessionClaims
	if err := json.Unmarshal(payload, &claims); err != nil || claims.UserID < 1 || claims.Expiry < time.Now().Unix() {
		return sessionClaims{}, errors.New("expired session")
	}
	return claims, nil
}

func (a *Auth) CSRF(claims sessionClaims) string {
	return base64.RawURLEncoding.EncodeToString(a.sign([]byte("csrf:" + claims.Nonce)))
}

func (a *Auth) ValidCSRF(claims sessionClaims, value string) bool {
	want := a.CSRF(claims)
	return subtle.ConstantTimeCompare([]byte(want), []byte(value)) == 1
}

func (a *Auth) IPHash(ip string) string {
	sum := hmac.New(sha256.New, a.secret)
	sum.Write([]byte("ip:" + ip))
	return base64.RawURLEncoding.EncodeToString(sum.Sum(nil))
}

func (a *Auth) sign(payload []byte) []byte {
	mac := hmac.New(sha256.New, a.secret)
	mac.Write(payload)
	return mac.Sum(nil)
}

func parseID(value string) int64 {
	id, _ := strconv.ParseInt(value, 10, 64)
	return id
}
