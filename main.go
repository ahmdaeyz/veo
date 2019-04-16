package main

import (
	"fmt"
	"log"
	"net/http"
	"os"

	"github.com/gocolly/colly"

	"github.com/ahmdaeyz/messenger"
	"github.com/paked/configure"
)

// a messenger bot to download videos from facebook :D
var links = map[int]string{
	1: "https://www.facebook.com/EgyptianEgg/videos/391878808308465/?__xts__%5B0%5D=68.ARCvwSJV8rFUYHqUD40iCPHxgN1YCN3H7_kWHZ4oaM8V0HZSY2TkzUiRnHutA6L8LTTM87d2NkGJh9YNvdFDdgi2QbI1mK-kAJHOliGb5jzmq4gAlcl_EmIXzf3BJXHRRcANevTufsWRT_hjmGzLLR6o0mHC942FILtbufU079fWnN_qzZjLDKXv9wOMtElqiAKGQx3aG-nFswgwLbVxitCgu5J8j43tgFvXMWseRc3s37pwPQl1d2IEORhQheHBCY1mIAksXL4yyeVuDutNttKfrEqx_yBZv08I7XpSFyfIrmWxKQuMPh9OMMXQ0131d8CwXipNtlNQi4MVI3NecYCfUl-El26qAOYzCzMgXRlj0xXh23Azwx-_umZV8abg2g&__tn__=-R",
}

type video struct {
	VideoID string `json:"videoID"`
	Src     string `json:"src"`
}

var (
	conf        = configure.New()
	verifyToken = conf.String("verify-token", "mad-skrilla", "The token used to verify facebook")
	verify      = conf.Bool("should-verify", false, "Whether or not the app should verify itself")
	pageToken   = conf.String("page-token", "not skrilla", "The token that is used to verify the page on facebook")
	client      *messenger.Messenger
)

func determineListenAddress() (string, error) {
	port := os.Getenv("PORT")
	if port == "" {
		return "", fmt.Errorf("$PORT not set")
	}
	return ":" + port, nil
}

func init() {
	conf.Use(configure.NewFlag())
	conf.Use(configure.NewEnvironment())
	conf.Use(configure.NewJSONFromFile("./config.json"))
}

func main() {
	conf.Parse()
	client = messenger.New(messenger.Options{
		Verify:      *verify,
		VerifyToken: *verifyToken,
		Token:       *pageToken,
	})
	client.HandleMessage(messages)

	fmt.Println("Serving messenger bot on localhost:8080")
	listeningAt, err := determineListenAddress()
	if err != nil {
		log.Fatal(err)
	}
	http.ListenAndServe(listeningAt, client.Handler())
}
func messages(m messenger.Message, r *messenger.Response) {
	log.Println(m.Attachments[0].URL)
	log.Println(m.Time)
	err := r.SenderAction("mark_seen")
	if err != nil {
		log.Fatal(err)
	}
	var videoLink string
	c := colly.NewCollector(
		colly.UserAgent("Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/72.0.3626.109 Safari/537.36"),
	)
	c.OnHTML("head > meta", func(e *colly.HTMLElement) {
		if e.Attr("property") == "og:video" {
			videoLink = e.Attr("content")
		}
	})
	err = c.Visit(m.Attachments[len(m.Attachments)-1].URL)
	if err != nil {
		log.Fatal(err)
	}
	err = r.Attachment(messenger.VideoAttachment, videoLink, messenger.ResponseType)
	if err != nil {
		log.Fatal(err)
	}

}

//document.getElementsByClassName("_53mw")[0].getAttribute("data-store").src
/*
type Attachment struct {


	URL string `json:"url,omitempty"`
	// Type is what type the message is. (image, video, audio or location)
	Type string `json:"type"`
	// Payload is the information for the file which was sent in the attachment.
	Payload Payload `json:"payload"`
}



*/
