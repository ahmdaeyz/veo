package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
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
	c = colly.NewCollector(
		colly.UserAgent("Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/72.0.3626.109 Safari/537.36"),
		colly.AllowURLRevisit(),
	)
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
	log.Fatal(http.ListenAndServe(listeningAt, client.Handler()))

}
func messages(m messenger.Message, r *messenger.Response) {
	switch{
	case len(m.Attachments) != 0 :
		isVideo, err := isVideo(m.Attachments[0].URL)
		if err != nil {
			_ = r.Text("Smth went wrong,Kindly try again", messenger.ResponseType)
			log.Fatal(err)
		}
		if isVideo {
			user := &user{}
			ctx, _ := context.WithTimeout(context.Background(), 10*time.Second)
			db, err := mongo.Connect(ctx, options.Client().ApplyURI(*mongoURI))
			if err != nil {
				_ = r.Text("Smth went wrong,Kindly try again", messenger.ResponseType)
				log.Fatal(err)
			}
			collection := db.Database("veo").Collection("users")
			update := collection.FindOneAndUpdate(ctx, bson.M{"user_id": m.Sender.ID}, bson.M{"$push": bson.M{"history": bson.M{"time": m.Time, "required_url": m.Attachments[len(m.Attachments)-1].URL}}})
			if update.Err() != nil {
				log.Println("error updating database", update.Err())
			}
			if update.Decode(&user) != nil {
				res, err := collection.InsertOne(ctx, bson.M{"user_id": m.Sender.ID, "history": bson.A{bson.M{"time": m.Time, "required_url": m.Attachments[len(m.Attachments)-1].URL}}})
				if err != nil {
					log.Println("error inserting document", err)
				}
				_ = res
			}
			dbUser := collection.FindOne(ctx, bson.M{"user_id": m.Sender.ID})
			err = dbUser.Decode(&user)
			if err != nil {
				log.Println("error decoding", err)
			}
			if len(user.History) >= 2 {
				if cmp.Equal(user.History[len(user.History)-1], user.History[len(user.History)-2]) {
					err = r.SenderAction("mark_seen")
					if err != nil {
						log.Println("error sending sender action : ", err)
					}
					if len(user.History) >= 3 {
						dropFirstEntry := collection.FindOneAndUpdate(ctx, bson.M{"user_id": m.Sender.ID}, bson.M{"$pop": bson.M{"history": -1}})
						if dropFirstEntry.Err() != nil {
							log.Println("error droping first entry of user history", dropFirstEntry.Err())
						}
					}
				} else {
					videoLink, err := scrapHead(m)
					if err != nil {
						_ = r.Text("Smth went wrong,Kindly try again", messenger.ResponseType)
						log.Fatal(err)
					}
					err = sendVidAttachment(r, videoLink)
					if err != nil {
						_ = r.Text("Smth went wrong,Kindly try again", messenger.ResponseType)
						log.Fatal(err)
					}
				}
			} else {
				videoLink, err := scrapHead(m)
				if err != nil {
					_ = r.Text("Smth went wrong,Kindly try again", messenger.ResponseType)
					log.Fatal(err)
				}
				err = sendVidAttachment(r, videoLink)
				if err != nil {
					_ = r.Text("Smth went wrong,Kindly try again", messenger.ResponseType)
					log.Fatal(err)
				}
			}
		} else {
			_ = r.Text("Please Share a Post that contains a video :)", messenger.ResponseType)
		}
	 case strings.Contains(m.Text, "https://www.facebook.com") :
		isVideo, err := isVideo(strings.TrimSpace(m.Text))
		if err != nil {
			_ = r.Text("Smth went wrong,Kindly try again", messenger.ResponseType)
			log.Fatal(err)
		}
		if isVideo || strings.Contains(m.Text,"comment_id") {
			user := &user{}
			ctx, _ := context.WithTimeout(context.Background(), 10*time.Second)
			db, err := mongo.Connect(ctx, options.Client().ApplyURI(*mongoURI))
			if err != nil {
				_ = r.Text("Smth went wrong,Kindly try again", messenger.ResponseType)
				log.Fatal(err)
			}
			collection := db.Database("veo").Collection("users")
			update := collection.FindOneAndUpdate(ctx, bson.M{"user_id": m.Sender.ID}, bson.M{"$push": bson.M{"history": bson.M{"time": m.Time, "required_url": m.Text}}})
			if update.Err() != nil {
				log.Println("error updating database", update.Err())
			}
			if update.Decode(&user) != nil {
				res, err := collection.InsertOne(ctx, bson.M{"user_id": m.Sender.ID, "history": bson.A{bson.M{"time": m.Time, "required_url": m.Text}}})
				if err != nil {
					log.Println("error inserting document", err)
				}
				_ = res
			}
			dbUser := collection.FindOne(ctx, bson.M{"user_id": m.Sender.ID})
			err = dbUser.Decode(&user)
			if err != nil {
				log.Println("error decoding", err)
			}
			if len(user.History) >= 2 {
				if cmp.Equal(user.History[len(user.History)-1], user.History[len(user.History)-2]) {
					err = r.SenderAction("mark_seen")
					if err != nil {
						log.Fatal("error sending sender action : ", err)
					}
					if len(user.History) >= 3 {
						dropFirstEntry := collection.FindOneAndUpdate(ctx, bson.M{"user_id": m.Sender.ID}, bson.M{"$pop": bson.M{"history": -1}})
						if dropFirstEntry.Err() != nil {
							log.Println("error droping first entry of user history", dropFirstEntry.Err())
						}
					}
				} else {
					videoLink, err := scrapMobileVidLink(m)
					if err != nil {
						_ = r.Text("Smth went wrong,Kindly try again", messenger.ResponseType)
						log.Fatal(err)
					}
					err = sendVidAttachment(r, videoLink)
					if err != nil {
						_ = r.Text("Smth went wrong,Kindly try again", messenger.ResponseType)
						log.Fatal(err)					}
				}
			} else {
				videoLink, err := scrapMobileVidLink(m)
				if err != nil {
					_ = r.Text("Smth went wrong,Kindly try again", messenger.ResponseType)
					log.Fatal(err)
				}
				err = sendVidAttachment(r, videoLink)
				if err != nil {
					_ = r.Text("Smth went wrong,Kindly try again", messenger.ResponseType)
					log.Fatal(err)
				}
			}
		} else {
			_ = r.Text("Please send a link that has a video.", messenger.ResponseType)
		}
	case strings.Contains(strings.ToLower(m.Text),"good bot"):
		_ = r.Text("3>", messenger.ResponseType)
	default:
		_ = r.Text(`Please share the requested video with "send as message","send in messenger" or provide the video url if so check if the sent url is valid`, messenger.ResponseType)
	}
}

func scrapMobileVidLink(m messenger.Message) (string, error) {
	var vidLink string
	c.OnHTML("._53mw", func(e *colly.HTMLElement) {
		value, _ := fastjson.Parse(e.Attr("data-store"))
		vidLink = strings.Replace(strings.Replace(value.Get("src").String(), "\\", "", -1), "\"", "", -1)
	})
	err :=c.Visit(strings.Replace(strings.TrimSpace(m.Text), "www", "m", -1))
	if err!=nil{
		return "", errors.Wrap(err,"error scraping mobile video link")
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
				//err = r.Text("Couldn't send the video But here is the download link 😉", messenger.ResponseType)
				//if err != nil {
				//	return errors.Wrap(err, "error sending wink text")
				//}
				//err = r.Text(videoLink, messenger.ResponseType)
				//if err != nil {
				//	return errors.Wrap(err, "error sending video link")
				//}
				errB := r.ButtonTemplate("Download",&[]messenger.StructuredMessageButton{{Type:"web_url",URL:videoLink,Title:"Download",WebviewHeightRatio:"compact"}},messenger.ResponseType)
				if errB!=nil{
					return errors.Wrap(err, "error sending button")
				}
				return errors.Wrap(err, "error sending attachment")
			}
		} else {
			err = r.Text("Requested Video Exceeds The Maximum Size Allowed By The Messenger Platform 😔", messenger.ResponseType)
			if err != nil {
				return errors.Wrap(err, "error sending exceed text")
			}
			err = r.Text("But here is the download link 😉", messenger.ResponseType)
			if err != nil {
				return errors.Wrap(err, "error sending wink text")
			}
			err = r.Text(videoLink, messenger.ResponseType)
			if err != nil {
				return errors.Wrap(err, "error sending video link")
			}
		}
	}else{
		_ = r.Text(`Please share the requested video with "send as message","send in messenger" or provide the video url if so check if the sent url is valid`, messenger.ResponseType)
	}
	return nil
}
func isVideo(URL string) (bool, error) {
	if strings.Contains(URL, "watch") || strings.Contains(URL, "videos") {
		return true, nil
	}
	client := http.Client{
		Timeout: 30 * time.Second,
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
