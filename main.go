package main

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/PuerkitoBio/goquery"
	"github.com/ahmdaeyz/messenger"
	"github.com/gocolly/colly"
	"github.com/google/go-cmp/cmp"
	"github.com/paked/configure"
	"github.com/pkg/errors"
	log "github.com/sirupsen/logrus"
	"github.com/valyala/fastjson"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

type record struct {
	Time        time.Time
	RequiredURL string
}
type user struct {
	UserID  int64
	History []record
}

var (
	conf        = configure.New()
	verifyToken = conf.String("verify-token", "verify token", "The token used to verify facebook")
	verify      = conf.Bool("should-verify", false, "Whether or not the app should verify itself")
	pageToken   = conf.String("page-token", "page token", "The token that is used to verify the page on facebook")
	mongoURI    = conf.String("mongo-uri", "mongodb uri", "MongoDB URI")
	client      *messenger.Messenger
	c           *colly.Collector
)

func init() {
	conf.Use(configure.NewFlag())
	conf.Use(configure.NewEnvironment())
	conf.Use(configure.NewJSONFromFile("./config.json"))
	c = colly.NewCollector(
		colly.UserAgent("Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/72.0.3626.109 Safari/537.36"),
		colly.AllowURLRevisit(),
	)
}
func determineListenAddress() (string, error) {
	port := os.Getenv("PORT")
	if port == "" {
		return "", fmt.Errorf("$PORT not set")
	}
	return ":" + port, nil
}
func main() {
	conf.Parse()
	client = messenger.New(messenger.Options{
		Verify:      *verify,
		VerifyToken: *verifyToken,
		Token:       *pageToken,
	})
	client.HandleMessage(messages)
	err := client.GreetingSetting(`Welcome to Veo!
You can use me as follows :
- Send me the public Facebook video link.
- If you are in desktop, Click "Share" => "Send as Message" and search for me.
- if you are on mobile, Tap "Share" => "Send in Messenger" and search for me.
If you faced any issues please ask me at ask.fm @ahmdaeyz or tweet me @ahmdaeyz.`)
	if err != nil {
		log.Println("Couldn't greet user", err)
	}
	fmt.Println("Serving messenger bot on localhost:8080")
	// listeningAt, err := determineListenAddress()
	// if err != nil {
	// 	log.Fatal(err)
	// }
	log.Fatal(http.ListenAndServe(":8080", client.Handler()))
}
func messages(m messenger.Message, r *messenger.Response) {
	videoURL, err := deduceURL(m)
	if err != nil {
		_ = r.Text("Please Provide a Valid Facebook Post/Attachment", messenger.ResponseType)
		log.WithFields(log.Fields{"Op": "deducing url from message", "Url": videoURL}).Warn(err)
	}
	isVideo, err := isVideo(videoURL)
	if err != nil {
		_ = r.Text("Please Provide a Facebook Post/Attachment Containing a Video", messenger.ResponseType)
		log.WithFields(log.Fields{"Op": "checking post has video", "Url": videoURL}).Warn(err)
	}
	if isVideo {
		user := handleUser(videoURL, r, m)
		if len(user.History) >= 2 {
			if cmp.Equal(user.History[len(user.History)-1], user.History[len(user.History)-2]) {
				err = r.SenderAction("typing_on")
				if err != nil {
					log.WithFields(log.Fields{"Op": "sending sender action", "type": "typing_on"}).Warn(err)
				}
			} else {
				err = r.SenderAction("typing_on")
				if err != nil {
					log.WithFields(log.Fields{"Op": "sending sender action", "type": "typing_on"}).Warn(err)
				}
				videoLink, err := scrapHead(m)
				if err != nil {
					log.WithFields(log.Fields{"Op": "scrapping video download link", "method": "from head", "Url": videoURL}).Warn(err)
					videoLink, err = scrapMobileVidLink(m)
					if err != nil {
						_ = r.Text("Something went wrong, Kindly Try Again", messenger.ResponseType)
						log.WithFields(log.Fields{"Op": "scrapping video download link", "method": "from mobile site", "Url": videoURL}).Warn(err)
						return
					}
					err = sendVidAttachment(r, videoLink)
					if err != nil {
						_ = r.Text("Something went wrong, Kindly Try Again", messenger.ResponseType)
						log.WithFields(log.Fields{"Op": "Sending Video Attachment", "method": "from mobile site", "DownloadLink": videoLink}).Warn(err)
						return
					}
				}
				err = sendVidAttachment(r, videoLink)
				if err != nil {
					_ = r.Text("Smth went wrong,Kindly try again", messenger.ResponseType)
					log.WithFields(log.Fields{"Op": "Sending Video Attachment", "method": "from head", "DownloadLink": videoLink}).Warn(err)
					return
				}
			}
		}
	}
}

func scrapMobileVidLink(m messenger.Message) (string, error) {
	var vidLink string
	c.OnHTML("._53mw", func(e *colly.HTMLElement) {
		value, _ := fastjson.Parse(e.Attr("data-store"))
		vidLink = strings.Replace(strings.Replace(value.Get("src").String(), "\\", "", -1), "\"", "", -1)
	})
	err := c.Visit(strings.Replace(strings.TrimSpace(m.Text), "www", "m", -1))
	if err != nil {
		return "", errors.Wrap(err, "error scraping mobile video link")
	}
	return vidLink, nil
}
func scrapHead(m messenger.Message) (string, error) {
	var vidLink string
	c.OnHTML("head > meta", func(e *colly.HTMLElement) {
		if e.Attr("property") == "og:video" {
			vidLink = e.Attr("content")
		}
	})
	err := c.Visit(m.Attachments[len(m.Attachments)-1].URL)
	if err != nil {
		return "", errors.Wrap(err, "error scrapping video link : ")
	}
	return vidLink, nil
}
func sendVidAttachment(r *messenger.Response, videoLink string) error {
	if videoLink != "" {
		client := http.Client{
			Timeout: 10 * time.Second,
		}
		res, err := client.Head(videoLink)
		videoLength, _ := strconv.Atoi(res.Header.Get("Content-Length"))
		if err != nil {
			return err
		}

		if videoLength <= 25000000 {
			err = r.Attachment(messenger.VideoAttachment, videoLink, messenger.ResponseType)
			if err != nil {
				errB := r.ButtonTemplate("", &[]messenger.StructuredMessageButton{{Type: "web_url", URL: videoLink, Title: "Download", WebviewHeightRatio: "full"}}, messenger.ResponseType)
				if errB != nil {
					return errors.Wrap(err, "error sending button")
				}
				log.Fatal(errors.Wrap(err, "error sending attachment"))
			}
		} else {
			errB := r.ButtonTemplate("", &[]messenger.StructuredMessageButton{{Type: "web_url", URL: videoLink, Title: "Download", WebviewHeightRatio: "full"}}, messenger.ResponseType)
			if errB != nil {
				log.Fatal(errors.Wrap(err, "error sending button"))
			}
		}
	} else {
		_ = r.Text(`Please share the requested video with "send as message","send in messenger" or provide the video url if so check if the sent url is valid`, messenger.ResponseType)
	}
	return nil
}

//isVideo Checks if a post has a video
func isVideo(URL string) (bool, error) {
	if strings.Contains(URL, "watch") || strings.Contains(URL, "videos") {
		return true, nil
	}
	client := http.Client{
		Timeout: 20 * time.Second,
	}
	res, err := client.Get(URL)
	if err != nil {
		return false, errors.Wrap(err, "error getting url")
	}
	defer res.Body.Close()
	doc, err := goquery.NewDocumentFromReader(res.Body)
	str, err := doc.Html()
	if err != nil {
		return false, errors.Wrap(err, "error decoding to html")
	}
	return strings.Contains(str, "_53j5"), nil
}

//handleUser Updates user's History and Adds one if doesn't exist
func handleUser(url string, r *messenger.Response, m messenger.Message) *user {
	user := &user{}
	ctx, _ := context.WithTimeout(context.Background(), 10*time.Second)
	db, err := mongo.Connect(ctx, options.Client().ApplyURI(*mongoURI))
	if err != nil {
		_ = r.Text("Something went wrong, Kindly Try Again", messenger.ResponseType)
		log.WithFields(log.Fields{"DB_Op": "connecting"}).Fatal(err)
	}
	collection := db.Database("veo").Collection("users")
	filter := bson.M{"user_id": m.Sender.ID}
	update := bson.M{"$push": bson.M{"history": bson.M{"time": m.Time, "required_url": url}}}
	updateUserHistory := collection.FindOneAndUpdate(ctx, filter, update)
	if updateUserHistory.Err() != nil {
		log.WithFields(log.Fields{"DB_Op": "updating user history"}).Warn(updateUserHistory.Err())
	}
	if updateUserHistory.Decode(&user) != nil {
		newUser := bson.M{"user_id": m.Sender.ID, "history": bson.A{bson.M{"time": m.Time, "required_url": url}}}
		insertNewUser, err := collection.InsertOne(ctx, newUser)
		if err != nil {
			log.WithFields(log.Fields{"DB_Op": "add new user & stack to history"}).Warn(err)
		}
		_ = insertNewUser
	}
	dbUser := collection.FindOne(ctx, bson.M{"user_id": m.Sender.ID})
	err = dbUser.Decode(&user)
	if err != nil {
		log.WithFields(log.Fields{"DB_Op": "finding user"}).Warn(fmt.Errorf("couldn't decode : %v", err))
	}
	return user
}

//deduceURL Deduces video URL from message
func deduceURL(m messenger.Message) (string, error) {
	if len(m.Attachments) != 0 {
		if validFacebookURL(m.Attachments[0].URL) {
			return m.Attachments[0].URL, nil
		}
	} else {
		if validFacebookURL(m.Text) {
			return m.Text, nil
		}
	}
	return "", fmt.Errorf("couldn't deduce url from message : %v", m)
}

//validFacebookURL Checks if a URL is a valid Facebook URL
func validFacebookURL(URL string) bool {
	u, err := url.Parse(URL)
	return err == nil && u.Scheme != "" && strings.Contains(u.Host,"facebook.com")
}
