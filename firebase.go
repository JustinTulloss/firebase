// Package firebase gives a thin wrapper around the firebase REST API. It tries
// to mirror the Official firebase API somewhat closely. https://www.firebase.com/docs/web/api/
package firebase

import (
	"bytes"
	"database/sql/driver"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"reflect"
	"regexp"
	"strconv"
	"time"

	"github.com/ancientlore/go-avltree"
	"github.com/facebookgo/httpcontrol"
)

var keyExtractor = regexp.MustCompile(`https://.*/([^/]+)/?$`)

// Timestamp is a time.Time with support for going from and to firebase
// ServerValue.TIMESTAMP fields.
//
// Thanks to Gal Ben-Haim for the inspiration
// https://medium.com/coding-and-deploying-in-the-cloud/time-stamps-in-golang-abcaf581b72f
type Timestamp time.Time

const milliDivider = 1000000

func (t *Timestamp) MarshalJSON() ([]byte, error) {
	ts := time.Time(*t).UnixNano() / milliDivider // Milliseconds
	stamp := fmt.Sprint(ts)

	return []byte(stamp), nil
}

func (t *Timestamp) UnmarshalJSON(b []byte) error {
	ts, err := strconv.ParseInt(string(b), 10, 64)
	if err != nil {
		return err
	}

	seconds := int64(ts) / 1000
	nanoseconds := (int64(ts) % 1000) * milliDivider
	*t = Timestamp(time.Unix(seconds, nanoseconds))

	return nil
}

func (t *Timestamp) Scan(src interface{}) error {
	fmt.Printf("is this even happening?\n")
	ts, ok := src.(time.Time)
	if !ok {
		return errors.New("Did not receive a time.Time type as src")
	}
	*t = Timestamp(ts)
	return nil
}

func (t *Timestamp) Value() (driver.Value, error) {
	if t == nil {
		return driver.Value(nil), nil
	}
	return driver.Value(time.Time(*t)), nil
}

func (t Timestamp) String() string {
	return time.Time(t).String()
}

type ServerValue struct {
	Value string `json:".sv"`
}

type FirebaseError struct {
	Message string `json:"error"`
}

func (f *FirebaseError) Error() string {
	return f.Message
}

// Use this value to represent a Firebase server timestamp in a data structure.
// This should be used when you're sending data to Firebase, as opposed to
// the Timestamp type.
var ServerTimestamp ServerValue = ServerValue{"timestamp"}

type KeyedValue struct {
	avltree.Pair
	OrderBy string
}

func (p *KeyedValue) GetComparable() reflect.Value {
	value := reflect.Indirect(reflect.ValueOf(p.Value))
	var comparable reflect.Value
	switch value.Kind() {
	case reflect.Map:
		comparable = value.MapIndex(reflect.ValueOf(p.OrderBy))
	case reflect.Struct:
		comparable = value.FieldByName(p.OrderBy)
	default:
		panic("Can only get comparable for maps and structs")
	}
	if comparable.Kind() == reflect.Interface || comparable.Kind() == reflect.Ptr {
		return comparable.Elem()
	}
	return comparable
}

func (a *KeyedValue) Compare(b avltree.Interface) int {
	if a.OrderBy == "" || a.OrderBy == KeyProp {
		return a.Pair.Compare(b.(*KeyedValue).Pair)
	}
	ac := a.GetComparable()
	bc := b.(*KeyedValue).GetComparable()
	if ac.Kind() != bc.Kind() {
		panic(fmt.Sprintf("Cannot compare %s to %s", ac.Kind(), bc.Kind()))
	}
	switch ac.Kind() {
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		ai := ac.Int()
		bi := bc.Int()
		if ai < bi {
			return -1
		} else if ai == bi {
			return 0
		} else if ai > bi {
			return 1
		}
	case reflect.Float32, reflect.Float64:
		af := ac.Float()
		bf := bc.Float()
		if af < bf {
			return -1
		} else if af == bf {
			return 0
		} else if af > bf {
			return 1
		}
	case reflect.String:
		as := ac.String()
		bs := bc.String()
		if as < bs {
			return -1
		} else if as == bs {
			return 0
		} else if as > bs {
			return 1
		}
	default:
		panic(fmt.Sprintf("Can only compare strings, floats, and ints. Not %s", ac.Kind()))
	}
	return 0
}

// A function that provides an interface to copy decoded data into in
// Client#Iterator
type Destination func() interface{}

// Api is the internal interface for interacting with Firebase.
// Consumers of this package can mock this interface for testing purposes, regular
// consumers can just use the default implementation and can ignore this completely.
// Arguments are as follows:
//  - `method`: The http method for this call
//  - `path`: The full firebase url to call
//  - `body`: Data to be marshalled to JSON (it's the responsibility of Call to do the marshalling and unmarshalling)
//  - `params`: Additional parameters to be passed to firebase
//  - `dest`: The object to save the unmarshalled response body to.
//    It's up to this method to unmarshal correctly, the default implemenation just uses `json.Unmarshal`
type Api interface {
	Call(method, path, auth string, body interface{}, params map[string]string, dest interface{}) error
}

type Client interface {
	// Returns the absolute URL path for the client
	String() string

	// Returns the last part of the URL path for the client.
	Key() string

	//Gets the value referenced by the client and unmarshals it into
	// the passed in destination.
	Value(destination interface{}) error

	// Iterator returns a channel that will emit objects in order defined by
	// Client#OrderBy
	Iterator(d Destination) <-chan *KeyedValue

	// Shallow returns a list of keys at a particular location
	// Only supports objects, unlike the REST artument which supports
	// literals. If the location is a literal, use Client#Value()
	Shallow() ([]string, error)

	// Child returns a reference to the child specified by `path`. This does not
	// actually make a request to firebase, but you can then manipulate the reference
	// by calling one of the other methods (such as `Value`, `Update`, or `Set`).
	Child(path string) Client

	// Query functions. They map directly to the Firebase operations.
	// https://www.firebase.com/docs/rest/guide/retrieving-data.html#section-rest-queries
	OrderBy(prop string) Client
	EqualTo(value string) Client
	StartAt(value string) Client
	EndAt(value string) Client
	LimitToFirst(value int) Client
	LimitToLast(value int) Client

	// Creates a new value under this reference.
	// Returns a reference to the newly created value.
	// https://www.firebase.com/docs/web/api/firebase/push.html
	Push(value interface{}, params map[string]string) (Client, error)

	// Overwrites the value at the specified path and returns a reference
	// that points to the path specified by `path`
	Set(path string, value interface{}, params map[string]string) (Client, error)

	// Update performs a partial update with the given value at the specified path.
	// Returns an error if the update could not be performed.
	// https://www.firebase.com/docs/web/api/firebase/update.html
	Update(path string, value interface{}, params map[string]string) error

	// Remove deletes the data at the current reference.
	// https://www.firebase.com/docs/web/api/firebase/remove.html
	Remove(path string, params map[string]string) error

	// Rules returns the security rules for the database.
	// https://www.firebase.com/docs/rest/api/#section-security-rules
	Rules(params map[string]string) (*Rules, error)

	// SetRules overwrites the existing security rules with the new rules given.
	// https://www.firebase.com/docs/rest/api/#section-security-rules
	SetRules(rules *Rules, params map[string]string) error
}

// This is the actual default implementation
type client struct {
	// The ordering being enforced on this client
	Order string
	// url is the client's base URL used for all calls.
	url string

	// auth is authentication token used when making calls.
	// The token is optional and can also be overwritten on an individual
	// call basis via params.
	auth string

	// api is the underlying client used to make calls.
	api Api

	params map[string]string
}

// Rules is the structure for security rules.
type Rules map[string]interface{}

// f is the internal implementation of the Firebase API client.
type f struct{}

var (
	connectTimeout   = time.Duration(30 * time.Second) // timeout for http connection
	readWriteTimeout = time.Duration(10 * time.Second) // timeout for http read/write
)

// httpClient is the HTTP client used to make calls to Firebase with the default API
var httpClient = newTimeoutClient(connectTimeout, readWriteTimeout)

func NewClient(root, auth string, api Api) Client {
	if api == nil {
		api = new(f)
	}

	return &client{url: root, auth: auth, api: api}
}

func (c *client) String() string {
	return c.url
}

func (c *client) Key() string {
	matches := keyExtractor.FindAllStringSubmatch(c.url, 1)
	// This is kind of an error. There should always be a / somewhere,
	// but if you just have the raw domain you don't really need one. So
	// we assume this is the case and return ""
	if len(matches) == 0 {
		return ""
	}
	return matches[0][1]
}

func (c *client) Value(destination interface{}) error {
	err := c.api.Call("GET", c.url, c.auth, nil, c.params, destination)
	if err != nil {
		return err
	}
	return nil
}

func (c *client) Iterator(d Destination) <-chan *KeyedValue {
	if d == nil {
		d = func() interface{} { return &map[string]interface{}{} }
	}
	out := make(chan *KeyedValue)
	go func() {
		tree := avltree.NewObjectTree(0)
		unorderedVal := map[string]json.RawMessage{}
		// XXX: What do we do in case of error?
		c.Value(&unorderedVal)
		for key, _ := range unorderedVal {
			destination := d()
			json.Unmarshal(unorderedVal[key], destination)
			tree.Add(&KeyedValue{
				Pair: avltree.Pair{
					Key:   key,
					Value: destination,
				},
				OrderBy: c.Order,
			})
		}
		for in := range tree.Iter() {
			out <- in.(*KeyedValue)
		}
		close(out)
	}()
	return out
}

func (c *client) Shallow() ([]string, error) {
	results := make(map[string]bool)

	err := c.clientWithNewParam("shallow", true).Value(&results)
	if err != nil {
		return nil, err
	}

	keySlice := make([]string, 0, len(results))

	for k, _ := range results {
		keySlice = append(keySlice, k)
	}

	return keySlice, nil
}

func (c *client) Child(path string) Client {
	u := c.url + "/" + path
	return &client{
		api:    c.api,
		auth:   c.auth,
		url:    u,
		params: c.params,
	}
}

const (
	KeyProp = "$key"
)

// These are some shenanigans, golang. Shenanigans I say.
func (c *client) newParamMap(key string, value interface{}) map[string]string {
	ret := make(map[string]string, len(c.params)+1)
	for key, value := range c.params {
		ret[key] = value
	}
	jsonVal, _ := json.Marshal(value)
	ret[key] = string(jsonVal)
	return ret
}

func (c *client) clientWithNewParam(key string, value interface{}) *client {
	return &client{
		api:    c.api,
		auth:   c.auth,
		url:    c.url,
		params: c.newParamMap(key, value),
	}
}

// Query functions. They map directly to the Firebase operations.
// https://www.firebase.com/docs/rest/guide/retrieving-data.html#section-rest-queries
func (c *client) OrderBy(prop string) Client {
	newC := c.clientWithNewParam("orderBy", prop)
	newC.Order = prop
	return newC
}

func (c *client) EqualTo(value string) Client {
	return c.clientWithNewParam("equalTo", value)
}

func (c *client) StartAt(value string) Client {
	return c.clientWithNewParam("startAt", value)
}

func (c *client) EndAt(value string) Client {
	return c.clientWithNewParam("endAt", value)
}

func (c *client) LimitToFirst(value int) Client {
	return c.clientWithNewParam("limitToFirst", value)
}

func (c *client) LimitToLast(value int) Client {
	return c.clientWithNewParam("limitToLast", value)
}

func (c *client) Push(value interface{}, params map[string]string) (Client, error) {
	res := map[string]string{}
	err := c.api.Call("POST", c.url, c.auth, value, params, &res)
	if err != nil {
		return nil, err
	}

	return &client{
		api:    c.api,
		auth:   c.auth,
		url:    c.url + "/" + res["name"],
		params: c.params,
	}, nil
}

func (c *client) Set(path string, value interface{}, params map[string]string) (Client, error) {
	u := c.url + "/" + path

	err := c.api.Call("PUT", u, c.auth, value, params, nil)
	if err != nil {
		return nil, err
	}

	return &client{
		api:    c.api,
		auth:   c.auth,
		url:    u,
		params: c.params,
	}, nil
}

func (c *client) Update(path string, value interface{}, params map[string]string) error {
	err := c.api.Call("PATCH", c.url+"/"+path, c.auth, value, params, nil)
	return err
}

func (c *client) Remove(path string, params map[string]string) error {
	err := c.api.Call("DELETE", c.url+"/"+path, c.auth, nil, params, nil)

	return err
}

func (c *client) Rules(params map[string]string) (*Rules, error) {
	res := &Rules{}
	err := c.api.Call("GET", c.url+"/.settings/rules", c.auth, nil, params, res)
	if err != nil {
		return nil, err
	}

	return res, nil
}

func (c *client) SetRules(rules *Rules, params map[string]string) error {
	err := c.api.Call("PUT", c.url+"/.settings/rules", c.auth, rules, params, nil)

	return err
}

// Call invokes the appropriate HTTP method on a given Firebase URL.
func (f *f) Call(method, path, auth string, body interface{}, params map[string]string, dest interface{}) error {

	// Every path needs to end in .json for the Firebase REST API
	path += ".json"
	qs := url.Values{}

	// if the client has an auth, set it as a query string.
	// the caller can also override this on a per-call basis
	// which will happen via params below
	if len(auth) > 0 {
		qs.Set("auth", auth)
	}

	for k, v := range params {
		qs.Set(k, v)
	}

	if len(qs) > 0 {
		path += "?" + qs.Encode()
	}

	encodedBody, err := json.Marshal(body)
	if err != nil {
		return err
	}

	req, err := http.NewRequest(method, path, bytes.NewReader(encodedBody))
	if err != nil {
		return err
	}

	req.Close = true

	res, err := httpClient.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()

	decoder := json.NewDecoder(res.Body)
	if res.StatusCode >= 400 {
		err := &FirebaseError{}
		decoder.Decode(err)
		return err
	}

	if dest != nil && res.ContentLength != 0 {
		err = decoder.Decode(dest)
		if err != nil {
			return err
		}
	}

	return nil
}

func newTimeoutClient(connectTimeout time.Duration, readWriteTimeout time.Duration) *http.Client {
	return &http.Client{
		Transport: &httpcontrol.Transport{
			RequestTimeout:      readWriteTimeout,
			DialTimeout:         connectTimeout,
			MaxTries:            3,
			MaxIdleConnsPerHost: 200,
		},
	}
}
