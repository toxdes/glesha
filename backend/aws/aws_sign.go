package aws

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"
)

func getCanonicalURI(req *http.Request) string {
	path := req.URL.Path
	if path == "" {
		return "/"
	}
	if !strings.HasPrefix("/", path) {
		path = "/" + path
	}

	return url.PathEscape(path)
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
func getHashedPayload(req *http.Request) (string, error) {
	var bodyBytes []byte

	if req.Body != nil {
		var err error
		bodyBytes, err = io.ReadAll(req.Body)
		if err != nil {
			return "", fmt.Errorf("failed to read request body: %w", err)
		}
		req.Body = io.NopCloser(bytes.NewReader(bodyBytes))
	}
	hash := sha256.Sum256(bodyBytes)

	return hex.EncodeToString(hash[:]), nil
}

func hmacSha256(key []byte, data []byte) []byte {
	h := hmac.New(sha256.New, key)
	h.Write(data)
	return h.Sum(nil)
}

func (aws *AwsBackend) SignRequest(req *http.Request) error {

	payloadHash, err := getHashedPayload(req)

	RequestDateTimeFormat := "20060102T150405Z"
	DateFormat := "20060102"
	now := time.Now().UTC()

	if err != nil {
		return err
	}

	req.Header.Set("x-amz-content-sha256", payloadHash)
	req.Header.Set("x-amz-date", now.Format(RequestDateTimeFormat))

	signedHeaders := getSignedHeaders(req)

	canonicalReq := fmt.Sprintf("%s\n%s\n%s\n%s\n%s\n%s",
		req.Method,
		getCanonicalURI(req),
		getCanonicalQueryString(req),
		getCanonicalHeaders(req),
		getSignedHeaders(req),
		payloadHash,
	)

	h := sha256.Sum256(bytes.NewBufferString(canonicalReq).Bytes())
	hashedCanonicalRequest := hex.EncodeToString(h[:])

	credentialScope := fmt.Sprintf("%s/%s/%s/aws4_request", now.Format(DateFormat), aws.region, "s3")

	stringToSign := fmt.Sprintf("%s\n%s\n%s\n%s",
		"AWS4-HMAC-SHA256",
		now.Format(RequestDateTimeFormat),
		credentialScope,
		hashedCanonicalRequest)

	dateKey := hmacSha256([]byte("AWS4"+aws.secretKey), []byte(now.Format(DateFormat)))
	dateRegionKey := hmacSha256(dateKey, []byte(aws.region))
	dateRegionServiceKey := hmacSha256(dateRegionKey, []byte("s3"))
	signingKey := hmacSha256(dateRegionServiceKey, []byte("aws4_request"))
	signature := hex.EncodeToString(hmacSha256(signingKey, []byte(stringToSign)))
	authHeader := fmt.Sprintf("%s Credential=%s/%s,SignedHeaders=%s,Signature=%s",
		"AWS4-HMAC-SHA256",
		aws.accessKey,
		credentialScope,
		signedHeaders,
		signature)
	req.Header.Set("Authorization", authHeader)
	return nil
}
