package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"github.com/dustin/go-humanize"
	"github.com/gorilla/feeds"
	"github.com/labstack/echo/v5"
	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase"
	"github.com/pocketbase/pocketbase/apis"
	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/tools/cron"
	"github.com/pocketbase/pocketbase/tools/template"
	"github.com/yuin/goldmark"
	gtemplate "html/template"
	"log"
	"math"
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
	Id           string  `db:"id"`
	Name         string  `db:"name"`
	Image        string  `db:"image"`
	Url          string  `db:"url"`
	MerchantId   string  `db:"merchant"`
	Tags         string  `db:"tags"`
	Interactions float32 `db:"interactions"`
	Updated      string  `db:"updated"`
	Created      string  `db:"created"`
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

func roundTo(n float64, decimals uint32) float64 {
	return math.Round(n*math.Pow(10, float64(decimals))) / math.Pow(10, float64(decimals))
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
		scheduler := cron.New()
		scheduler.MustAdd("hello", "0 0 * * *", func() {
			var products []Product

			err := app.Dao().DB().
				Select("*").
				From("products").
				All(&products)

			if err != nil {
				panic(err)
			}

			for _, product := range products {

				_, err = app.Dao().
					DB().
					Update("products",
						dbx.Params{"interactions": roundTo(float64(product.Interactions*0.9), 2)},
						dbx.NewExp("id = {:id}",
							dbx.Params{"id": product.Id})).Execute()

				if err != nil {
					print(err)
				}
			}

			fmt.Printf("Drop score of %d products\n", len(products))
		})
		scheduler.Start()

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

		e.Router.GET("/rss/:feed", func(c echo.Context) error {
			feedId := c.PathParam("feed")

			if feedId != "products/new" {
				return apis.NewNotFoundError("", nil)
			}

			now := time.Now()
			feed := &feeds.Feed{
				Title:       "BoosterOven.com New Products",
				Link:        &feeds.Link{Href: "https://boosteroven.com/sort/new"},
				Description: "All the new S-Tier Tinkerer Tech added to BoosterOven.com",
				Author:      &feeds.Author{Name: "Abe Haskins", Email: "abeisgreat@abeisgreat.com"},
				Created:     now,
			}

			var products []Product

			err := app.Dao().DB().
				Select("*").
				From("products").
				OrderBy("created DESC").
				Limit(20).
				All(&products)

			if err != nil {
				panic(err)
			}

			for _, product := range products {
				created, _ := time.Parse(layout, product.Created)
				feed.Items = append(feed.Items, &feeds.Item{
					Title:       product.Name,
					Id:          product.Id,
					Link:        &feeds.Link{Href: "https://boosteroven.com/links/" + product.Id},
					Description: "New item added: " + product.Name,
					Author:      &feeds.Author{Name: "Abraham Haskins", Email: "abeisgreat@abeisgreat.com"},
					Created:     created,
				})
			}

			rss, err := feed.ToRss()
			if err != nil {
				log.Fatal(err)
			}

			return c.String(http.StatusOK, rss)
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
