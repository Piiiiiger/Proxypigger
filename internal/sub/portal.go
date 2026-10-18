package sub

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/mhsanaei/3x-ui/v3/internal/database"
	"github.com/mhsanaei/3x-ui/v3/internal/database/model"
	"github.com/mhsanaei/3x-ui/v3/internal/logger"
	"github.com/mhsanaei/3x-ui/v3/internal/util/clientip"
	"github.com/mhsanaei/3x-ui/v3/internal/web/service"
)

const (
	portalCookieName = "x_ui_portal"
	portalSessionTTL = 30 * 24 * time.Hour
	portalDailyDays  = 30
	portalBodyLimit  = 4 << 10

	// Forwarded addresses can be varied by the client, so failures are also capped
	// per username whatever address they claim.
	portalUserMaxFailures = 10
	portalUserWindow      = 15 * time.Minute
	portalAnyAddress      = "*"
	// Code guesses count per address under one name, so trying names in turn gains nothing.
	portalCodeGuesses = "*code*"
)

// portalSession is what the signed cookie carries: who signed in, until when,
// and the tag of the password they signed in with.
type portalSession struct {
	ClientId int
	Expires  int64
	Tag      string
}

// portalPlan is the person's plan by name, with the limits they actually have.
type portalPlan struct {
	Name            string `json:"name"`
	TotalGB         int64  `json:"totalGB"`
	TrafficReset    string `json:"trafficReset"`
	TrafficResetDay int    `json:"trafficResetDay"`
	LimitIP         int    `json:"limitIp"`
}

type portalData struct {
	TelegramBot   string                   `json:"telegramBot"`
	Account       *model.AccountActivation `json:"account"`
	TelegramBound bool                     `json:"telegramBound"`
	Email         string                   `json:"email"`
	Page          map[string]any           `json:"page"`
	Plan          *portalPlan              `json:"plan"`
	Daily         []service.TrafficDay     `json:"daily"`
	// Probe offers the probe view: set when one of the client's hosts is linked.
	Probe bool `json:"probe"`
}

func (a *SUBController) portalPath() string {
	return a.subPath + "portal"
}

// portalKey derives the cookie key from the panel secret, so rotating the
// secret signs every client out.
func (a *SUBController) portalKey() ([]byte, error) {
	secret, err := a.settingService.GetSecret()
	if err != nil {
		return nil, err
	}
	mac := hmac.New(sha256.New, secret)
	mac.Write([]byte("x-ui subscription portal session v1"))
	return mac.Sum(nil), nil
}

func portalMAC(key []byte, payload string) []byte {
	mac := hmac.New(sha256.New, key)
	mac.Write([]byte(payload))
	return mac.Sum(nil)
}

func (a *SUBController) signPortalSession(s portalSession) (string, error) {
	key, err := a.portalKey()
	if err != nil {
		return "", err
	}
	payload := fmt.Sprintf("v1.%d.%d.%s", s.ClientId, s.Expires, s.Tag)
	return payload + "." + base64.RawURLEncoding.EncodeToString(portalMAC(key, payload)), nil
}

func (a *SUBController) readPortalSession(value string, now time.Time) (portalSession, bool) {
	cut := strings.LastIndexByte(value, '.')
	if cut < 0 {
		return portalSession{}, false
	}
	payload, sig := value[:cut], value[cut+1:]
	key, err := a.portalKey()
	if err != nil {
		return portalSession{}, false
	}
	got, err := base64.RawURLEncoding.DecodeString(sig)
	if err != nil || !hmac.Equal(got, portalMAC(key, payload)) {
		return portalSession{}, false
	}
	parts := strings.Split(payload, ".")
	if len(parts) != 4 || parts[0] != "v1" {
		return portalSession{}, false
	}
	id, idErr := strconv.Atoi(parts[1])
	expires, expErr := strconv.ParseInt(parts[2], 10, 64)
	if idErr != nil || expErr != nil || now.Unix() >= expires {
		return portalSession{}, false
	}
	return portalSession{ClientId: id, Expires: expires, Tag: parts[3]}, true
}

func (a *SUBController) portalClientIP(c *gin.Context) string {
	trusted, err := a.settingService.GetTrustedProxyCIDRs()
	if err != nil || strings.TrimSpace(trusted) == "" {
		trusted = service.DefaultTrustedProxyCIDRs
	}
	return clientip.FromRequest(c.Request.RemoteAddr, c.GetHeader("X-Real-IP"), c.GetHeader("X-Forwarded-For"), trusted)
}

func (a *SUBController) setPortalCookie(c *gin.Context, value string, maxAge int) {
	secure := c.Request.TLS != nil ||
		(a.subService.forwardedHeadersTrusted(c) && strings.EqualFold(c.GetHeader("X-Forwarded-Proto"), "https"))
	http.SetCookie(c.Writer, &http.Cookie{
		Name:     portalCookieName,
		Value:    value,
		Path:     a.portalPath(),
		MaxAge:   maxAge,
		HttpOnly: true,
		Secure:   secure,
		SameSite: http.SameSiteLaxMode,
	})
}

// portalPage serves the subscription page bundle in portal mode; the page then
// asks portal/data whether someone is signed in.
func (a *SUBController) portalPage(c *gin.Context) {
	basePath, _ := c.Get("base_path")
	basePathStr, _ := basePath.(string)
	if basePathStr == "" {
		basePathStr = "/"
	}
	body, err := subPageHTML(basePathStr)
	if err != nil {
		c.String(http.StatusInternalServerError, "missing embedded subpage")
		return
	}
	portal, _ := json.Marshal(map[string]string{"base": a.portalPath()})
	inject := []byte(`<script>window.X_UI_BASE_PATH="` + jsStringEscaper.Replace(basePathStr) + `";` +
		`window.__SUB_PORTAL__=` + string(portal) + `;</script></head>`)
	setNoCacheHeaders(c)
	c.Data(http.StatusOK, "text/html; charset=utf-8", bytes.Replace(body, []byte("</head>"), inject, 1))
}

type portalLoginForm struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

func portalJSON(c *gin.Context) {
	if c.ContentType() != "application/json" {
		c.AbortWithStatusJSON(http.StatusUnsupportedMediaType, gin.H{"error": "invalid"})
		return
	}
	c.Next()
}

func (a *SUBController) portalLogin(c *gin.Context) {
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, portalBodyLimit)
	var form portalLoginForm
	if err := c.ShouldBindJSON(&form); err != nil || form.Username == "" || form.Password == "" {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "error": "invalid"})
		return
	}
	ip := a.portalClientIP(c)
	_, ipOK := a.portalLimiter.Allow(ip, form.Username)
	_, userOK := a.portalUserCap.Allow(portalAnyAddress, form.Username)
	if !ipOK || !userOK {
		logger.Warningf("portal: sign-in for %q from %s refused, too many failures", form.Username, ip)
		c.JSON(http.StatusTooManyRequests, gin.H{"success": false, "error": "blocked"})
		return
	}
	client, tag, err := a.portalService.Authenticate(form.Username, form.Password)
	if errors.Is(err, service.ErrPortalLogin) {
		a.portalLimiter.RegisterFailure(ip, form.Username)
		a.portalUserCap.RegisterFailure(portalAnyAddress, form.Username)
		logger.Warningf("portal: failed sign-in for %q from %s", form.Username, ip)
		c.JSON(http.StatusUnauthorized, gin.H{"success": false, "error": "invalid"})
		return
	}
	if err != nil {
		logger.Warning("portal: sign-in failed:", err)
		c.JSON(http.StatusInternalServerError, gin.H{"success": false, "error": "server"})
		return
	}
	a.portalLimiter.RegisterSuccess(ip, form.Username)
	a.portalUserCap.RegisterSuccess(portalAnyAddress, form.Username)
	if err := a.startPortalSession(c, client.Id, tag); err != nil {
		logger.Warning("portal: could not sign the session:", err)
		c.JSON(http.StatusInternalServerError, gin.H{"success": false, "error": "server"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true})
}

// startPortalSession signs a session for the client and sets its cookie.
func (a *SUBController) startPortalSession(c *gin.Context, clientId int, tag string) error {
	value, err := a.signPortalSession(portalSession{
		ClientId: clientId,
		Expires:  time.Now().Add(portalSessionTTL).Unix(),
		Tag:      tag,
	})
	if err != nil {
		return err
	}
	a.setPortalCookie(c, value, int(portalSessionTTL.Seconds()))
	return nil
}

type portalRegisterForm struct {
	Username string `json:"username"`
	Password string `json:"password"`
	Code     string `json:"code"`
}

// portalRegister makes an account from an activation code and signs it in.
func (a *SUBController) portalRegister(c *gin.Context) {
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, portalBodyLimit)
	var form portalRegisterForm
	if err := c.ShouldBindJSON(&form); err != nil || form.Username == "" || form.Password == "" || form.Code == "" {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "error": "invalid"})
		return
	}
	ip := a.portalClientIP(c)
	if _, ok := a.portalLimiter.Allow(ip, portalCodeGuesses); !ok {
		c.JSON(http.StatusTooManyRequests, gin.H{"success": false, "error": "blocked"})
		return
	}
	var client *model.ClientRecord
	var needRestart bool
	err := a.abuseService.Signup(ip, time.Now(), func() (string, error) {
		var err error
		client, needRestart, err = a.codeService.Register(&service.InboundService{}, form.Username, form.Password, form.Code)
		if err != nil {
			return "", err
		}
		return client.Email, nil
	})
	if needRestart {
		(&service.XrayService{}).SetToNeedRestart()
	}
	if errors.Is(err, service.ErrSignupLimit) {
		logger.Warningf("portal: sign-up from %s refused, its network reached the day's limit", ip)
		c.JSON(http.StatusTooManyRequests, gin.H{"success": false, "error": "signup_limit"})
		return
	}
	if !a.answerCodeError(c, ip, err) {
		return
	}
	logger.Infof("portal: %q registered from %s", client.Email, ip)
	_, tag, err := a.portalService.Authenticate(client.Email, form.Password)
	if err == nil {
		err = a.startPortalSession(c, client.Id, tag)
	}
	if err != nil {
		logger.Warning("portal: could not sign the new user in:", err)
		c.JSON(http.StatusInternalServerError, gin.H{"success": false, "error": "server"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true})
}

// portalRedeem is retained for old browsers, but account credentials are no
// longer consumable renewal vouchers. Administration owns renewals.
func (a *SUBController) portalRedeem(c *gin.Context) {
	if _, ok := a.portalSessionClient(c); !ok {
		return
	}
	c.JSON(http.StatusConflict, gin.H{"success": false, "error": "account_code", "message": "激活码固定对应账号，续费请联系管理员。"})
}

// answerCodeError answers a failed register or redeem and reports whether it went
// through; a wrong code counts against the address like a wrong password.
func (a *SUBController) answerCodeError(c *gin.Context, ip string, err error) bool {
	switch {
	case err == nil:
		return true
	case errors.Is(err, service.ErrActivationCode):
		a.portalLimiter.RegisterFailure(ip, portalCodeGuesses)
		logger.Warningf("portal: wrong activation code from %s", ip)
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "error": "code"})
	case errors.Is(err, service.ErrUsernameTaken):
		c.JSON(http.StatusConflict, gin.H{"success": false, "error": "taken"})
	default:
		logger.Warning("portal: activation code not applied:", err)
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "error": "invalid"})
	}
	return false
}

func (a *SUBController) portalLogout(c *gin.Context) {
	a.setPortalCookie(c, "", -1)
	c.JSON(http.StatusOK, gin.H{"success": true})
}

// portalSessionClient returns the client the session cookie names. Without a
// valid session it answers the request itself and reports false.
func (a *SUBController) portalSessionClient(c *gin.Context) (*model.ClientRecord, bool) {
	value, err := c.Cookie(portalCookieName)
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return nil, false
	}
	session, ok := a.readPortalSession(value, time.Now())
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return nil, false
	}
	client, err := a.portalService.SessionClient(session.ClientId, session.Tag)
	if errors.Is(err, service.ErrPortalLogin) {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
		return nil, false
	}
	if err != nil {
		logger.Warning("portal: could not load the session's client:", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "server"})
		return nil, false
	}
	return client, true
}

func (a *SUBController) portalData(c *gin.Context) {
	setNoCacheHeaders(c)
	client, ok := a.portalSessionClient(c)
	if !ok {
		return
	}

	data := portalData{Email: client.Email, TelegramBound: client.TgID != 0, TelegramBot: a.settingService.AccountBotUsername()}
	var accountErr error
	data.Account, accountErr = service.EnsureAccountActivation(client)
	if accountErr != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "account"})
		return
	}
	page, found, err := a.pageDataFor(c, client.SubID)
	if err != nil {
		logger.Warning("portal: could not build the subscription page:", err)
	}
	if found {
		data.Page = a.subPageContext(page)
	}
	if client.PlanId > 0 {
		var plan model.Plan
		if err := database.GetDB().First(&plan, client.PlanId).Error; err == nil {
			data.Plan = &portalPlan{
				Name:            plan.Name,
				TotalGB:         client.TotalGB,
				TrafficReset:    client.TrafficReset,
				TrafficResetDay: client.TrafficResetDay,
				LimitIP:         client.LimitIP,
			}
		}
	}
	loc, err := a.settingService.GetTimeLocation()
	if err != nil {
		loc = time.Local
	}
	if data.Daily, err = a.statsService.ClientDaily(client.Email, time.Now().In(loc), portalDailyDays); err != nil {
		logger.Warning("portal: could not load daily traffic:", err)
		data.Daily = []service.TrafficDay{}
	}
	if data.Probe, err = a.probeService.ClientHasLinkedHost(client); err != nil {
		logger.Warning("portal: could not check the client's probe links:", err)
	}
	c.JSON(http.StatusOK, data)
}

// portalOnlineIps shows the signed-in client how many of its IP slots are in use,
// the networks online and the ones banned for going over; the page polls it.
func (a *SUBController) portalOnlineIps(c *gin.Context) {
	setNoCacheHeaders(c)
	client, ok := a.portalSessionClient(c)
	if !ok {
		return
	}
	ips, err := a.ipLimitService.OnlineIps(client.Email, time.Now())
	if err != nil {
		logger.Warning("portal: could not load the client's online IPs:", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "online_ips"})
		return
	}
	c.JSON(http.StatusOK, ips)
}

// portalBans returns the signed-in client's standing and its bans of the last
// 90 days, IP-limit ones included, so people can see when and why.
func (a *SUBController) portalBans(c *gin.Context) {
	setNoCacheHeaders(c)
	client, ok := a.portalSessionClient(c)
	if !ok {
		return
	}
	history, err := a.abuseService.HistoryOf(client.Email, time.Now())
	if err != nil {
		logger.Warning("portal: could not load the client's bans:", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "bans"})
		return
	}
	c.JSON(http.StatusOK, history)
}

func (a *SUBController) portalAIPatrolSeen(c *gin.Context) {
	setNoCacheHeaders(c)
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, portalBodyLimit)
	client, ok := a.portalSessionClient(c)
	if !ok {
		return
	}
	var req struct {
		IDs []int `json:"ids"`
	}
	if err := c.ShouldBindJSON(&req); err != nil || len(req.IDs) > 100 {
		c.Status(http.StatusBadRequest)
		return
	}
	if err := (&service.AIPatrolService{}).Seen(client.Email, req.IDs, time.Now()); err != nil {
		c.Status(http.StatusInternalServerError)
		return
	}
	c.Status(http.StatusNoContent)
}

// portalProbe returns the status of the servers behind the signed-in client's
// own inbounds; the page polls it while the probe view is open.
func (a *SUBController) portalProbe(c *gin.Context) {
	setNoCacheHeaders(c)
	client, ok := a.portalSessionClient(c)
	if !ok {
		return
	}
	probe, err := a.probeService.ClientServers(c.Request.Context(), client)
	if err != nil {
		// A client that stopped waiting is not a fault worth a log line.
		if !errors.Is(err, context.Canceled) {
			logger.Warning("portal: could not load the client's servers:", err)
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "server"})
		return
	}
	c.JSON(http.StatusOK, probe)
}
