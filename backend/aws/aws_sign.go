package aws

import (
	"bytes"
	"crypto/hmac"
	"fmt"
	"glesha/checksum"
	"glesha/database"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"
)

func hmacSha256(key []byte, data []byte) []byte {
	h := hmac.New(checksum.NewSha256, key)
	h.Write(data)
	return h.Sum(nil)
}

func getCanonicalURI(req *http.Request) string {
	path := req.URL.Path
	if path == "" {
		return "/"
	}
	if !strings.HasPrefix("/", path) {
		path = "/" + path
	}
	segments := strings.Split(path, "/")
	var fixedSegments []string
	for _, segment := range segments {
		if segment != "" {
			fixedSegments = append(fixedSegments, url.PathEscape(segment))
		}
	}
	return "/" + strings.Join(fixedSegments, "/")
}

func getCanonicalQueryString(req *http.Request) string {
	queryValues := req.URL.Query()

	if len(queryValues) == 0 {
		return ""
	}
	var keys []string
	for k := range queryValues {
		keys = append(keys, k)
	}

	var canonicalQueryParts []string
	for _, key := range keys {
		values := queryValues[key]
		sort.Strings(values)
		for _, val := range values {
			encodedKey := url.QueryEscape(key)
			encodedVal := url.QueryEscape(val)
			canonicalQueryParts = append(canonicalQueryParts, encodedKey+"="+encodedVal)
		}
	}
	sort.Strings(canonicalQueryParts)
	return strings.Join(canonicalQueryParts, "&")
}

func getCanonicalHeaders(req *http.Request) string {
	canonicalHeaders := make(map[string][]string)

	for name, values := range req.Header {
		lowerName := strings.ToLower(name)
		var processedValues []string

		for _, value := range values {
			trimmedValue := strings.TrimSpace(value)
			singleSpaceValue := strings.Join(strings.Fields(trimmedValue), " ")
			processedValues = append(processedValues, singleSpaceValue)
		}
		sort.Strings(processedValues)
		canonicalHeaders[lowerName] = processedValues
	}

	var sortedHeaderNames []string
	for name := range canonicalHeaders {
		sortedHeaderNames = append(sortedHeaderNames, name)
	}
	sort.Strings(sortedHeaderNames)

	var headerParts []string
	for _, name := range sortedHeaderNames {
		values := canonicalHeaders[name]
		joinedValues := strings.Join(values, ",")
		headerParts = append(headerParts, fmt.Sprintf("%s:%s", name, joinedValues))
	}

	return strings.Join(headerParts, "\n") + "\n"
}

func getSignedHeaders(req *http.Request) string {
	var signedHeaderNames []string
	for name := range req.Header {
		signedHeaderNames = append(signedHeaderNames, strings.ToLower(name))
	}
	sort.Strings(signedHeaderNames)
	return strings.Join(signedHeaderNames, ";")
}

// https://docs.aws.amazon.com/AmazonS3/latest/API/sig-v4-header-based-auth.html
func (aws *AwsBackend) signRequest(req *http.Request, payloadHash string) error {
	DateFormat := "20060102"
	now := time.Now().UTC()

	req.Header.Set("x-amz-content-sha256", payloadHash)
	req.Header.Set("x-amz-date", now.Format(database.DateTimeFormat))

	signedHeaders := getSignedHeaders(req)
	canonicalReq := fmt.Sprintf("%s\n%s\n%s\n%s\n%s\n%s",
		req.Method,
		getCanonicalURI(req),
		getCanonicalQueryString(req),
		getCanonicalHeaders(req),
		getSignedHeaders(req),
		payloadHash,
	)

	h := checksum.Sha256(bytes.NewBufferString(canonicalReq).Bytes())
	hashedCanonicalRequest := checksum.HexEncodeStr(h[:])

	credentialScope := fmt.Sprintf("%s/%s/%s/aws4_request", now.Format(DateFormat), aws.region, "s3")

	stringToSign := fmt.Sprintf("%s\n%s\n%s\n%s",
		"AWS4-HMAC-SHA256",
		now.Format(database.DateTimeFormat),
		credentialScope,
		hashedCanonicalRequest)

	dateKey := hmacSha256([]byte("AWS4"+aws.secretKey), []byte(now.Format(DateFormat)))
	dateRegionKey := hmacSha256(dateKey, []byte(aws.region))
	dateRegionServiceKey := hmacSha256(dateRegionKey, []byte("s3"))
	signingKey := hmacSha256(dateRegionServiceKey, []byte("aws4_request"))
	signature := checksum.HexEncodeStr(hmacSha256(signingKey, []byte(stringToSign)))
	authHeader := fmt.Sprintf("%s Credential=%s/%s,SignedHeaders=%s,Signature=%s",
		"AWS4-HMAC-SHA256",
		aws.accessKey,
		credentialScope,
		signedHeaders,
		signature)
	req.Header.Set("Authorization", authHeader)
	return nil
}
