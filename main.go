package main

import (
	"bytes"
	"encoding/json"
	"github.com/dustin/go-humanize"
	"github.com/labstack/echo/v5"
	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase"
	"github.com/pocketbase/pocketbase/apis"
	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/tools/template"
	"github.com/yuin/goldmark"
	gtemplate "html/template"
	"log"
	"net/http"
	"os"
	"strings"
	"time"
)

var registry *template.Registry
var layout = "2006-01-02 15:04:05.999Z"

func registryMiddleware(next echo.HandlerFunc) echo.HandlerFunc {

	return func(c echo.Context) error {
		if registry == nil || os.Getenv("PB_DEV") == "true" {
			registry = template.NewRegistry()

			registry.AddFuncs(map[string]any{
				"md":        toMarkdown,
				"toTagList": toTagList,
				"toIcon":    toIcon,
				"toTimeAgo": toTimeAgo,
			})
		}

		return next(c) // proceed with the request chain
	}
}

type Merchant struct {
	Id        string `db:"id"`
	Name      string `db:"name"`
	Affiliate bool   `db:"affiliate"`
	Label     string `db:"label"`
}

type Tag struct {
	Id    string `db:"id"`
	Name  string `db:"name"`
	Color string `db:"color"`
}

type Product struct {
	Id           string `db:"id"`
	Name         string `db:"name"`
	Image        string `db:"image"`
	Url          string `db:"url"`
	MerchantId   string `db:"merchant"`
	Tags         string `db:"tags"`
	Interactions int    `db:"interactions"`
	Updated      string `db:"updated"`
	Created      string `db:"created"`
}

var tags = make(map[string]Tag)
var merchants = make(map[string]Merchant)
var app = pocketbase.New()

func toMarkdown(s string) gtemplate.HTML {
	var buf bytes.Buffer
	if err := goldmark.Convert([]byte(s), &buf); err != nil {
		panic(err)
	}
	return gtemplate.HTML(buf.String())
}

func toTimeAgo(tStr string) string {
	t, err := time.Parse(layout, tStr)
	if err != nil {
		return "awhile ago"
	}
	return humanize.Time(t)
}

func toIcon(s string) gtemplate.HTML {
	buf, err := os.ReadFile("./assets/icons/" + s + ".svg")
	if err != nil {
		panic(err)
	}
	return gtemplate.HTML(string(buf))
}

func toTagList(tagStr string) []Tag {
	var tagList []string
	var tagStructList []Tag

	_ = json.Unmarshal([]byte(tagStr), &tagList)

	for _, tagId := range tagList {
		tag := tags[tagId]
		if tag.Name == "" {
			err := app.Dao().DB().
				Select("*").
				From("tags").
				Where(dbx.NewExp("id = {:id}", dbx.Params{"id": tagId})).
				One(&tag)

			if err != nil {
				panic(err)
			}

			tags[tagId] = tag
		}
		tagStructList = append(tagStructList, tags[tagId])
	}

	return tagStructList
}

type PageProductConfig struct {
	Minimal   bool
	Dated     bool
	Query     string
	Filenames []string
}

func pageProduct(products []Product, config PageProductConfig) string {
	for _, product := range products {
		toTimeAgo(product.Updated)
		merchant := merchants[product.MerchantId]
		if merchant.Name == "" {
			err := app.Dao().DB().
				Select("*").
				From("merchants").
				Where(dbx.NewExp("id = {:id}", dbx.Params{"id": product.MerchantId})).
				One(&merchant)

			if err != nil {
				panic(err)
			}

			merchants[product.MerchantId] = merchant
		}
	}

	var filenames []string
	filenames = append(filenames, "views/layout.html", "views/products.html")
	filenames = append(filenames, config.Filenames...)

	html, err := registry.LoadFiles(filenames...).Render(map[string]any{
		"products":  products,
		"merchants": merchants,
		"tags":      tags,
		"config":    config,
	})

	if err != nil {
		panic(err)
	}

	return html
}

func main() {
	app.OnBeforeServe().Add(func(e *core.ServeEvent) error {
		e.Router.Use(registryMiddleware)
		e.Router.GET("/assets/*", apis.StaticDirectoryHandler(os.DirFS("./assets"), false))

		e.Router.GET("/docs/:doc", func(c echo.Context) error {
			doc := c.PathParam("doc")
			doc = strings.ReplaceAll(doc, ".", "")
			buf, err := os.ReadFile("./docs/" + doc + ".md")

			if err != nil {
				panic(err)
			}

			html, err := registry.LoadFiles(
				"views/layout.html",
				"views/markdown.html",
			).Render(map[string]any{
				"markdown": string(buf),
			})

			if err != nil {
				return apis.NewNotFoundError("", err)
			}

			return c.HTML(http.StatusOK, html)
		})

		e.Router.GET("/link/:productId", func(c echo.Context) error {
			productId := c.PathParam("productId")

			var product Product
			err := app.Dao().DB().
				Select("*").
				From("products").
				Where(dbx.NewExp("id = {:id}", dbx.Params{"id": productId})).
				One(&product)

			if err != nil {
				panic(err)
			}

			_, err = app.Dao().
				DB().
				Update("products",
					dbx.Params{"interactions": product.Interactions + 1},
					dbx.NewExp("id = {:id}",
						dbx.Params{"id": productId})).Execute()

			if err != nil {
				print(err)
			}

			return c.Redirect(301, product.Url)
		})

		e.Router.GET("/search", func(c echo.Context) error {
			query := c.QueryParam("q")
			var products []Product

			code := strings.ToUpper(query)
			err := app.Dao().DB().
				Select("*").
				From("products").
				Where(dbx.NewExp("shortcode = {:code}", dbx.Params{"code": code})).
				OrWhere(dbx.NewExp("name LIKE {:query}", dbx.Params{"query": "%" + query + "%"})).
				All(&products)

			if err != nil {
				panic(err)
			}

			config := PageProductConfig{
				Filenames: []string{},
				Minimal:   false,
				Dated:     false,
				Query:     query,
			}
			return c.HTML(http.StatusOK, pageProduct(products, config))
		})

		e.Router.GET("/", func(c echo.Context) error {
			var products []Product

			err := app.Dao().DB().
				Select("*").
				From("products").OrderBy("interactions DESC").All(&products)

			if err != nil {
				panic(err)
			}

			config := PageProductConfig{
				Filenames: []string{"views/highlight_top.html"},
				Minimal:   false,
				Dated:     false,
			}
			return c.HTML(http.StatusOK, pageProduct(products, config))
		}, apis.ActivityLogger(app))

		e.Router.GET("/sort/:criteria", func(c echo.Context) error {
			criteria := c.PathParam("criteria")
			var products []Product

			query := app.Dao().DB().
				Select("*").
				From("products")

			config := PageProductConfig{
				Filenames: []string{},
				Minimal:   false,
				Dated:     false,
			}

			if criteria == "top" {
				query = query.OrderBy("interactions DESC")
				config.Filenames = []string{
					"views/highlight_top.html",
				}
			} else if criteria == "new" {
				query = query.OrderBy("created DESC")
				config.Minimal = true
				config.Filenames = []string{
					"views/highlight_new.html",
				}
				config.Dated = true
			}

			err := query.All(&products)

			if err != nil {
				panic(err)
			}

			return c.HTML(http.StatusOK, pageProduct(products, config))
		}, apis.ActivityLogger(app))

		e.Router.GET("/tags/:tagName", func(c echo.Context) error {
			var products []Product
			tagName := c.PathParam("tagName")

			var tag Tag
			err := app.Dao().DB().
				Select("*").
				From("tags").
				Where(dbx.NewExp("name = {:name}", dbx.Params{"name": tagName})).
				One(&tag)

			err = app.Dao().DB().
				Select("*").
				From("products").
				Where(dbx.Like("tags", tag.Id)).
				All(&products)

			if err != nil {
				panic(err)
			}

			config := PageProductConfig{
				Filenames: []string{},
				Minimal:   false,
				Dated:     false,
			}
			return c.HTML(http.StatusOK, pageProduct(products, config))

		}, apis.ActivityLogger(app))

		return nil
	})

	if err := app.Start(); err != nil {
		log.Fatal(err)
	}
}
