package main

import (
	"bytes"
	"context"
	"fmt"
	"html/template"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/alecthomas/chroma/formatters/html"
	"github.com/alecthomas/chroma/lexers"
	"github.com/alecthomas/chroma/styles"
	"github.com/apex/log"
	jsonhandler "github.com/apex/log/handlers/json"
	"github.com/apex/log/handlers/text"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/aws/endpoints"
	"github.com/aws/aws-sdk-go-v2/aws/external"
	"github.com/aws/aws-sdk-go-v2/service/cloudwatchlogs"
	"github.com/gorilla/mux"
	"github.com/gorilla/schema"
	"github.com/tidwall/pretty"
	login "github.com/unee-t/internal-github-login"
)

var views = template.Must(template.ParseGlob("templates/*.html"))
var decoder = schema.NewDecoder()

func main() {

	addr := ":" + os.Getenv("PORT")

	var app *mux.Router

	if os.Getenv("UP_STAGE") == "" {
		// i.e. local development
		log.SetHandler(text.Default)
		app = mux.NewRouter()
	} else {
		app = login.GithubOrgOnly() // sets up github callbacks
		app.Use(login.RequireUneeT)
		log.SetHandler(jsonhandler.Default)
	}

	app.HandleFunc("/", index)
	app.HandleFunc("/l", makeCanonical)
	app.HandleFunc("/q", loglookup)

	if err := http.ListenAndServe(addr, app); err != nil {
		log.WithError(err).Fatal("error listening")
	}

}

func index(w http.ResponseWriter, r *http.Request) {
	err := views.ExecuteTemplate(w, "index.html", nil)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

func makeCanonical(w http.ResponseWriter, r *http.Request) {
	uuid := strings.TrimSpace(r.URL.Query().Get("uuid"))
	reqid := strings.TrimSpace(r.URL.Query().Get("reqid"))

	since := r.URL.Query().Get("since")
	hours, err := strconv.Atoi(since)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	startEpoch := time.Now().Add(-time.Hour * time.Duration(hours)).Unix()
	endEpoch := time.Now().Unix()
	log.WithField("from", fmt.Sprintf("%d", startEpoch)).Infof("last %d hours", hours)

	v := url.Values{}
	v.Add("start", fmt.Sprintf("%d", startEpoch))
	v.Add("end", fmt.Sprintf("%d", endEpoch))
	if uuid != "" {
		v.Add("uuid", uuid)
	} else if reqid != "" {
		v.Add("reqid", reqid)
	}
	http.Redirect(w, r, "/q?"+v.Encode(), http.StatusFound)
}

func loglookup(w http.ResponseWriter, r *http.Request) {

	type Lookup struct {
		UUID  string
		ReqID string
		Start int64
		End   int64
	}

	var args Lookup

	if err := decoder.Decode(&args, r.URL.Query()); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	filterPattern := `{ $.level = "error" }`
	if args.UUID != "" {
		filterPattern = fmt.Sprintf(`{ $.fields.evt.mefeAPIRequestId = "%s" }`, args.UUID)
	}
	if args.ReqID != "" {
		filterPattern = fmt.Sprintf(`{ $.fields.requestID = "%s" }`, args.ReqID)
	}

	cfg, err := external.LoadDefaultAWSConfig(external.WithSharedConfigProfile("uneet-dev"))
	if err != nil {
		log.WithError(err).Fatal("setting up credentials")
		return
	}
	cfg.Region = endpoints.ApSoutheast1RegionID
	svc := cloudwatchlogs.New(cfg)

	startTime := args.Start * 1000
	endTime := args.End * 1000

	req := svc.FilterLogEventsRequest(&cloudwatchlogs.FilterLogEventsInput{
		EndTime:       &endTime,
		FilterPattern: aws.String(filterPattern),
		LogGroupName:  aws.String("/aws/lambda/alambda_simple"),
		StartTime:     &startTime,
	})

	var logs []template.HTML

	lexer := lexers.Get("json")
	style := styles.Get("monokai")
	formatter := html.New(html.WithClasses(), html.WithLineNumbers())
	css := &bytes.Buffer{}
	err = formatter.WriteCSS(css, style)
	if err != nil {
		log.WithError(err).Error("writing css")
	}

	p := cloudwatchlogs.NewFilterLogEventsPaginator(req)
	for p.Next(context.TODO()) {
		page := p.CurrentPage()
		for _, event := range page.Events {
			w := &bytes.Buffer{}
			contents := pretty.Pretty([]byte(*event.Message))
			iterator, err := lexer.Tokenise(nil, string(contents))
			err = formatter.Format(w, style, iterator)
			if err != nil {
				log.WithError(err).Error("woops")
			}
			logs = append(logs, template.HTML(w.String()))
		}
	}
	if err = p.Err(); err != nil {
		panic(err)
	}

	err = views.ExecuteTemplate(w, "logoutput.html", struct {
		Logs  []template.HTML
		CSS   template.CSS
		UUID  string
		ReqID string
		Start time.Time
		End   time.Time
	}{
		logs,
		template.CSS(css.String()),
		args.UUID,
		args.ReqID,
		time.Unix(args.Start, 0),
		time.Unix(args.End, 0),
	})
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}

}
