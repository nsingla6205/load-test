package utils

import (
	"fmt"
	"net/url"
	"strconv"
)

// PostgresDSN builds a Postgres connection URL from DbConfig. When isAdmin is
// true the admin credentials are used. Exported so components that need a raw
// connection outside the shared GORM pool (e.g. a LISTEN/NOTIFY session) can
// derive the same DSN the persistence layer uses.
func PostgresDSN(config DbConfig, isAdmin bool) (string, error) {
	var username, password string
	if isAdmin {
		username = config.AdminUser
		password = config.AdminPassword
	} else {
		username = config.User
		password = config.Password
	}

	query := url.Values{}
	query.Add("sslmode", config.SSLMode)
	query.Add("connect_timeout", strconv.Itoa(config.ConnectionTimeout))
	query.Add("timezone", config.TimeZone)

	scheme := config.Type
	if scheme == "" {
		scheme = Postgres
	}

	u := &url.URL{
		Scheme:   scheme,
		User:     url.UserPassword(username, password),
		Host:     fmt.Sprintf("%s:%s", config.Host, config.Port),
		Path:     config.Name,
		RawQuery: query.Encode(),
	}

	return u.String(), nil
}
