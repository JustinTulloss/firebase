package firebase_test

import (
	"encoding/json"
	"fmt"
	"os"
	"reflect"
	"testing"
	"time"

	"github.com/JustinTulloss/firebase"
	"github.com/JustinTulloss/firebase/mock_firebase"
	"github.com/golang/mock/gomock"
)

type Name struct {
	First string `json:",omitempty"`
	Last  string `json:",omitempty"`
}

func nameAlloc() interface{} {
	return &Name{}
}

/*
Set the two variables below and set them to your own
Firebase URL and credentials (optional) if you're forking the code
and want to test your changes.
*/

// enter your firebase credentials for testing.
var (
	testUrl  string = os.Getenv("FIREBASE_TEST_URL")
	testAuth string = os.Getenv("FIREBASE_TEST_AUTH")
)

func TestValue(t *testing.T) {
	client := firebase.NewClient(testUrl+"/tests", testAuth, nil)

	var r map[string]interface{}
	err := client.Value(&r)
	if err != nil {
		t.Error(err)
	}

	if r == nil {
		t.Fatalf("No values returned from the server\n")
	}
}

func TestChild(t *testing.T) {
	client := firebase.NewClient(testUrl+"/tests", testAuth, nil)

	r := client.Child("")

	if r == nil {
		t.Fatalf("No child returned from the server\n")
	}
}

func TestPush(t *testing.T) {
	client := firebase.NewClient(testUrl+"/tests", testAuth, nil)

	name := &Name{First: "FirstName", Last: "LastName"}

	r, err := client.Push(name, nil)

	if err != nil {
		t.Fatalf("%v\n", err)
	}

	if r == nil {
		t.Fatalf("No client returned from the server\n")
	}

	newName := &Name{}
	c2 := firebase.NewClient(r.String(), testAuth, nil)
	c2.Value(newName)
	if !reflect.DeepEqual(name, newName) {
		t.Errorf("Expected %v to equal %v", name, newName)
	}
}

func TestSet(t *testing.T) {
	c1 := firebase.NewClient(testUrl+"/tests/users", testAuth, nil)

	name := &Name{First: "First", Last: "last"}
	c2, _ := c1.Push(name, nil)

	newName := &Name{First: "NewFirst", Last: "NewLast"}
	r, err := c2.Set("", newName, map[string]string{"print": "silent"})

	if err != nil {
		t.Fatalf("%v\n", err)
	}

	if r == nil {
		t.Fatalf("No client returned from the server\n")
	}
}

func TestUpdate(t *testing.T) {
	c1 := firebase.NewClient(testUrl+"/tests/users", testAuth, nil)

	name := &Name{First: "First", Last: "last"}
	c2, _ := c1.Push(name, nil)

	newName := &Name{Last: "NewLast"}
	err := c2.Update("", newName, nil)

	if err != nil {
		t.Fatalf("%v\n", err)
	}
}

func TestRemovet(t *testing.T) {
	c1 := firebase.NewClient(testUrl+"/tests/users", testAuth, nil)

	name := &Name{First: "First", Last: "last"}
	c2, _ := c1.Push(name, nil)

	err := c2.Remove("", nil)
	if err != nil {
		t.Fatalf("%v\n", err)
	}

	var val map[string]interface{}
	c3 := firebase.NewClient(c2.String(), testAuth, nil)
	err = c3.Value(&val)
	if err != nil {
		t.Error(err)
	}

	if len(val) != 0 {
		t.Errorf("Expected %s to be removed, was %v", c2.String(), val)
	}
}

func TestRules(t *testing.T) {
	client := firebase.NewClient(testUrl, testAuth, nil)

	r, err := client.Rules(nil)

	if err != nil {
		t.Fatalf("Error retrieving rules: %v\n", err)
	}

	if r == nil {
		t.Fatalf("No child returned from the server\n")
	}
}

func TestSetRules(t *testing.T) {
	client := firebase.NewClient(testUrl, testAuth, nil)

	rules := &firebase.Rules{
		"rules": map[string]interface{}{
			".read":  "auth.username == 'admin'",
			".write": "auth.username == 'admin'",
			"ordered": map[string]interface{}{
				".indexOn": []string{"First"},
				"kids": map[string]interface{}{
					".indexOn": []string{"Age"},
				},
			},
		},
	}

	err := client.SetRules(rules, nil)

	if err != nil {
		t.Fatalf("Error setting rules: %v\n", err)
	}
}

func TestOrderBy(t *testing.T) {
	client := firebase.NewClient(testUrl, testAuth, nil).Child("ordered")
	defer client.Remove("", nil)

	names := []*Name{
		&Name{First: "BBBB", Last: "YYYY"},
		&Name{First: "AAAA", Last: "ZZZZZ"},
	}

	for _, n := range names {
		_, err := client.Push(n, nil)
		if err != nil {
			t.Fatalf("Couldn't push new name: %s\n", err)
		}
	}

	i := 0
	for n := range client.OrderBy(firebase.KeyProp).Iterator(nameAlloc) {
		if n.Value.(*Name).First != names[i].First {
			t.Fatalf("Key order was not delivered")
		}
		i++
	}
	if i == 0 {
		t.Fatalf("Did not receive names ordered by key")
	}

	expectedOrder := []*Name{names[1], names[0]}
	i = 0
	for n := range client.OrderBy("First").Iterator(nameAlloc) {
		if n.Value.(*Name).First != expectedOrder[i].First {
			t.Fatalf("Child prop order was not delivered")
		}
		i++
	}
	if i == 0 {
		t.Fatalf("Did not receive names ordered by first name")
	}

	kids := map[string]map[string]interface{}{
		"a": map[string]interface{}{"Name": "Bob", "Age": 14},
		"b": map[string]interface{}{"Name": "Alice", "Age": 13},
	}
	_, err := client.Set("kids", kids, nil)
	if err != nil {
		t.Fatalf("Could not set kids: %s\n", err)
	}

	expectedKidsOrder := []map[string]interface{}{kids["b"], kids["a"]}
	i = 0
	for n := range client.Child("kids").OrderBy("Age").Iterator(nil) {
		if (*n.Value.(*map[string]interface{}))["First"] != expectedKidsOrder[i]["First"] {
			t.Fatalf("Child prop order by age was not delivered")
		}
		i++
	}
	if i == 0 {
		t.Fatalf("Did not receive names ordered by age")
	}
}

func TestTimestamp(t *testing.T) {
	ts := firebase.Timestamp(time.Now())
	marshaled, err := json.Marshal(&ts)
	if err != nil {
		t.Fatalf("Could not marshal a timestamp to json: %s\n", err)
	}
	unmarshaledTs := firebase.Timestamp{}
	err = json.Unmarshal(marshaled, &unmarshaledTs)
	if err != nil {
		t.Fatalf("Could not unmarshal a timestamp to json: %s\n", err)
	}
	// Compare unix timestamps as we lose some fidelity in the nanoseconds
	if time.Time(ts).Unix() != time.Time(unmarshaledTs).Unix() {
		t.Fatalf("Unmarshaled time %s not equivalent to marshaled time %s",
			unmarshaledTs,
			ts,
		)
	}
}

func TestServerTimestamp(t *testing.T) {
	b, err := json.Marshal(firebase.ServerTimestamp)
	if err != nil {
		t.Fatalf("Could not marshal server timestamp: %s\n", err)
	}
	if string(b) != `{".sv":"timestamp"}` {
		t.Fatalf("Unexpected timestamp json value: %s\n", b)
	}
}

func TestIterator(t *testing.T) {
	client := firebase.NewClient(testUrl+"/test-iterator", testAuth, nil)
	defer client.Remove("", nil)
	names := []Name{
		{First: "FirstName", Last: "LastName"},
		{First: "Second", Last: "Seconder"},
	}
	for _, name := range names {
		_, err := client.Push(name, nil)
		if err != nil {
			t.Fatalf("%v\n", err)
		}
	}

	var i = 0
	for nameEntry := range client.Iterator(nameAlloc) {
		name := nameEntry.Value.(*Name)
		if !reflect.DeepEqual(&names[i], name) {
			t.Errorf("Expected %v to equal %v", &names[i], name)
		}
		i++
	}
	if i != len(names) {
		t.Fatalf("Did not receive all names, received %d\n", i)
	}
}

func TestShallow(t *testing.T) {
	client := firebase.NewClient(testUrl+"/test-shallow", testAuth, nil)
	defer client.Remove("", nil)
	client.Set("one", 1, nil)
	client.Set("two", 2, nil)
	keys, err := client.Shallow()
	if err != nil {
		t.Errorf("Error when calling shallow: %s\n", err)
	}
	if !reflect.DeepEqual(keys, []string{"one", "two"}) {
		t.Errorf("keys (%v) were not correct\n", keys)
	}
}

func TestKey(t *testing.T) {
	client := firebase.NewClient(testUrl+"/test", testAuth, nil)
	if client.Key() != "test" {
		t.Errorf("Key should have been test, was %s\n", client.Key())
	}

	client = firebase.NewClient(testUrl, testAuth, nil)
	if client.Key() != "" {
		t.Errorf("Key should have been empty, was %s\n", client.Key())
	}

	client = client.Child("/a/b/c/d/e/f/g")
	if client.Key() != "g" {
		t.Errorf("Key should have been 'g', was %s\n", client.Key())
	}
}

func TestMockable(t *testing.T) {
	mockCtrl := gomock.NewController(t)
	defer mockCtrl.Finish()

	mockFire := mock_firebase.NewMockClient(mockCtrl)
	mockFire.EXPECT().Child("test")
	mockFire.Child("test")
}

func TestMain(m *testing.M) {
	if testUrl == "" || testAuth == "" {
		fmt.Printf("You need to set FIREBASE_TEST_URL and FIREBASE_TEST_AUTH\n")
		os.Exit(1)
	}
	os.Exit(m.Run())
}
