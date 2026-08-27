package httpserver

import "github.com/labstack/echo/v4"

type Router struct {
	name string
	*echo.Group
}

func (r *Router) Name() string { return r.name }
