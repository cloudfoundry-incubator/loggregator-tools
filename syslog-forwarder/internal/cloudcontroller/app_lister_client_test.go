package cloudcontroller_test

import (
	"errors"

	"code.cloudfoundry.org/loggregator-tools/syslog-forwarder/internal/cloudcontroller"
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
)

var _ = Describe("AppListerClient", func() {
	var (
		curler *stubCurler
		c      *cloudcontroller.AppListerClient
	)

	BeforeEach(func() {
		curler = newStubCurler()
		c = cloudcontroller.NewAppListerClient(curler)
	})

	It("requests all apps in the space", func() {
		curler.resps["/v2/apps?q=space_guid:some-space"] = `
		{
			"resources": [
			{
				"metadata":{"guid":"a"},
				"entity": {"name":"app-1"}
			},
			{
				"metadata":{"guid":"b"},
				"entity": {"name":"app-2"}
			}
			]
		}
		`
		apps, err := c.ListApps("some-space")
		Expect(err).ToNot(HaveOccurred())
		Expect(curler.methods).To(ConsistOf("GET"))
		Expect(curler.URLs).To(ConsistOf("/v2/apps?q=space_guid:some-space"))
		Expect(apps).To(ConsistOf(
			cloudcontroller.App{
				Name: "app-1",
				Guid: "a",
			},
			cloudcontroller.App{
				Name: "app-2",
				Guid: "b",
			}))
	})

	It("returns an error if the GET fails", func() {
		curler.errs["/v2/apps?q=space_guid:some-space"] = errors.New("some-error")
		_, err := c.ListApps("some-space")
		Expect(err).To(MatchError("some-error"))
	})

	It("returns an error if the JSON is invalid", func() {
		curler.resps["/v2/apps?q=space_guid:some-space"] = `invalid`
		_, err := c.ListApps("some-space")
		Expect(err).To(HaveOccurred())
	})
})

type stubCurler struct {
	URLs    []string
	methods []string
	bodies  []string
	resps   map[string]string
	errs    map[string]error
}

func newStubCurler() *stubCurler {
	return &stubCurler{
		resps: make(map[string]string),
		errs:  make(map[string]error),
	}
}

func (s *stubCurler) Curl(URL, method, body string) ([]byte, error) {
	s.URLs = append(s.URLs, URL)
	s.methods = append(s.methods, method)
	s.bodies = append(s.bodies, body)
	return []byte(s.resps[URL]), s.errs[URL]
}
