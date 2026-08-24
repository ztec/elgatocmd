package elgato

import (
	"context"
	"reflect"
	"testing"
)

type fakeTransport struct {
	want     string
	response string
}

func (f fakeTransport) Request(_ context.Context, payload []byte) ([]byte, error) {
	if string(payload) != f.want {
		panic("request = " + string(payload) + ", want " + f.want)
	}
	return []byte(f.response), nil
}

type sequenceTransport struct {
	t         *testing.T
	requests  []string
	responses []string
	next      int
}

func (f *sequenceTransport) Request(_ context.Context, payload []byte) ([]byte, error) {
	f.t.Helper()
	if f.next >= len(f.requests) {
		f.t.Fatalf("unexpected extra request %q", payload)
	}
	if string(payload) != f.requests[f.next] {
		f.t.Fatalf("request %d = %q, want %q", f.next, payload, f.requests[f.next])
	}
	response := f.responses[f.next]
	f.next++
	return []byte(response), nil
}

func TestStatus(t *testing.T) {
	client := NewClient(fakeTransport{
		want:     "GET /elgato/lights",
		response: `{"numberOfLights":1,"lights":[{"on":1,"brightness":40,"temperature":200}]}`,
	})
	status, err := client.Status(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	want := Status{NumberOfLights: 1, Lights: []Light{{On: 1, Brightness: 40, Temperature: 200}}}
	if !reflect.DeepEqual(status, want) {
		t.Fatalf("status = %#v, want %#v", status, want)
	}
}

func TestSetPower(t *testing.T) {
	client := NewClient(fakeTransport{
		want:     `PUT /elgato/lights {"lights":[{"on":0}]}`,
		response: `{"numberOfLights":1,"lights":[{"on":0,"brightness":20,"temperature":250}]}`,
	})
	status, err := client.SetPower(context.Background(), false)
	if err != nil {
		t.Fatal(err)
	}
	if status.Lights[0].On != 0 {
		t.Fatalf("on = %d, want 0", status.Lights[0].On)
	}
}

func TestAtomicUpdate(t *testing.T) {
	on := true
	brightness := 30
	temperature := 5000
	client := NewClient(fakeTransport{
		want:     `PUT /elgato/lights {"lights":[{"on":1,"brightness":30,"temperature":200}]}`,
		response: `{"numberOfLights":1,"lights":[{"on":1,"brightness":30,"temperature":200}]}`,
	})
	status, err := client.Update(context.Background(), Update{
		On: &on, Brightness: &brightness, Temperature: &temperature,
	})
	if err != nil {
		t.Fatal(err)
	}
	if status.Lights[0].On != 1 || status.Lights[0].Brightness != 30 || status.Lights[0].Temperature != 200 {
		t.Fatalf("status = %#v", status)
	}
}

func TestAtomicUpdateRejectsEmpty(t *testing.T) {
	client := NewClient(fakeTransport{})
	if _, err := client.Update(context.Background(), Update{}); err == nil {
		t.Fatal("empty update was accepted")
	}
}

func TestKelvinConversion(t *testing.T) {
	tests := []struct {
		kelvin int
		mired  int
	}{
		{2900, 344},
		{5000, 200},
		{7000, 143},
	}
	for _, test := range tests {
		got, err := KelvinToMired(test.kelvin)
		if err != nil {
			t.Fatal(err)
		}
		if got != test.mired {
			t.Errorf("KelvinToMired(%d) = %d, want %d", test.kelvin, got, test.mired)
		}
	}
	if _, err := KelvinToMired(2500); err == nil {
		t.Fatal("out-of-range temperature was accepted")
	}
}

func TestApplyPreset(t *testing.T) {
	transport := &sequenceTransport{
		t: t,
		requests: []string{
			"GET /elgato/lights/settings",
			`PUT /elgato/lights {"lights":[{"on":1,"brightness":30,"temperature":344}]}`,
		},
		responses: []string{
			`{"remoteControl":{"favourites":[{"on":1,"brightness":30,"temperature":143},{"on":1,"brightness":30,"temperature":344}]}}`,
			`{"numberOfLights":1,"lights":[{"on":1,"brightness":30,"temperature":344}]}`,
		},
	}
	client := NewClient(transport)
	status, err := client.ApplyPreset(context.Background(), 2)
	if err != nil {
		t.Fatal(err)
	}
	if status.Lights[0].Temperature != 344 {
		t.Fatalf("temperature = %d, want 344", status.Lights[0].Temperature)
	}
}
