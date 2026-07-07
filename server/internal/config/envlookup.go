package config

import "strconv"

// AsEnvLookup returns an env-lookup closure that returns the merged
// file+env value for keys this Config models, and falls through to
// `base` (typically os.Getenv) for every other key. This lets
// sub-packages that already accept an `env func(string) string` keep
// working unchanged after a value moves from env to YAML.
//
// Precedence:
//
//  1. Config field; if it's the typed zero value, fall through to
//     base so unsetting in YAML does not override env-only callers.
//  2. base(key) for any key not modeled here.
func (c Config) AsEnvLookup(base EnvFunc) EnvFunc {
	if base == nil {
		base = func(string) string { return "" }
	}
	values := c.envSnapshot()
	return func(k string) string {
		if v, ok := values[k]; ok && v != "" {
			return v
		}
		return base(k)
	}
}

// envSnapshot renders the typed Config as a flat env map. Empty
// fields are omitted so AsEnvLookup falls through to `base`.
func (c Config) envSnapshot() map[string]string {
	m := map[string]string{}
	put := func(k, v string) {
		if v != "" {
			m[k] = v
		}
	}
	putBool := func(k string, v bool) {
		// Empty = unset (we can't tell "operator set false" from
		// "operator never set it" through the typed Config).
		if v {
			m[k] = "true"
		}
	}

	put(EnvAddr, c.Server.Addr)
	put(EnvPublicURL, c.Server.PublicURL)
	put(EnvDataDir, c.Server.DataDir)
	put(EnvDatabaseURL, c.Database.URL)

	putBool(EnvDevAuth, c.Auth.DevAuth)
	putBool(EnvCookieSecure, c.Auth.Cookie.Secure)

	put(EnvMasterKey, c.Secret.MasterKey)

	putBool(EnvFeishuMock, c.Gateway.Feishu.Mock)
	put(EnvFeishuAppID, c.Gateway.Feishu.AppID)
	put(EnvFeishuAppSecret, c.Gateway.Feishu.AppSecret)
	put(EnvFeishuRedirectURI, c.Gateway.Feishu.RedirectURI)
	put(EnvFeishuScope, c.Gateway.Feishu.Scope)
	put(EnvFeishuAuthorizeBase, c.Gateway.Feishu.AuthorizeBase)
	put(EnvFeishuAPIBase, c.Gateway.Feishu.APIBase)
	put(EnvFeishuVerificationToken, c.Gateway.Feishu.VerificationToken)
	put(EnvFeishuEncryptKey, c.Gateway.Feishu.EncryptKey)
	put(EnvLoginRedirectURL, c.Gateway.Feishu.LoginRedirectURL)

	put(EnvOpenCodeBin, c.Model.OpenCodeBin)
	put(EnvOpenCodeRunner, c.Sandbox.Runner)

	return m
}

// Reserved for future numeric env serialisation; keeps strconv imported.
var _ = strconv.Itoa
