// Package middleware - cloudfront wrapper for storage libs
// N.B. currently only works with S3, not arbitrary sites
//
package middleware

import (
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"io/ioutil"
	"time"

	"github.com/AdRoll/goamz/cloudfront"
	"github.com/docker/distribution/context"
	storagedriver "github.com/docker/distribution/registry/storage/driver"
	storagemiddleware "github.com/docker/distribution/registry/storage/driver/middleware"
)

// cloudFrontStorageMiddleware provides an simple implementation of layerHandler that
// constructs temporary signed CloudFront URLs from the storagedriver layer URL,
// then issues HTTP Temporary Redirects to this CloudFront content URL.
type cloudFrontStorageMiddleware struct {
	storagedriver.StorageDriver
	cloudfront *cloudfront.CloudFront
	duration   time.Duration
}

var _ storagedriver.StorageDriver = &cloudFrontStorageMiddleware{}

// newCloudFrontLayerHandler constructs and returns a new CloudFront
// LayerHandler implementation.
// Required options: baseurl, privatekey, keypairid
func newCloudFrontStorageMiddleware(storageDriver storagedriver.StorageDriver, options map[string]interface{}) (storagedriver.StorageDriver, error) {
	base, ok := options["baseurl"]
	if !ok {
		return nil, fmt.Errorf("No baseurl provided")
	}
	baseURL, ok := base.(string)
	if !ok {
		return nil, fmt.Errorf("baseurl must be a string")
	}
	pk, ok := options["privatekey"]
	if !ok {
		return nil, fmt.Errorf("No privatekey provided")
	}
	pkPath, ok := pk.(string)
	if !ok {
		return nil, fmt.Errorf("privatekey must be a string")
	}
	kpid, ok := options["keypairid"]
	if !ok {
		return nil, fmt.Errorf("No keypairid provided")
	}
	keypairID, ok := kpid.(string)
	if !ok {
		return nil, fmt.Errorf("keypairid must be a string")
	}

	pkBytes, err := ioutil.ReadFile(pkPath)
	if err != nil {
		return nil, fmt.Errorf("Failed to read privatekey file: %s", err)
	}

	block, _ := pem.Decode([]byte(pkBytes))
	if block == nil {
		return nil, fmt.Errorf("Failed to decode private key as an rsa private key")
	}
	privateKey, err := x509.ParsePKCS1PrivateKey(block.Bytes)
	if err != nil {
		return nil, err
	}

	cf := cloudfront.New(baseURL, privateKey, keypairID)

	duration := 20 * time.Minute
	d, ok := options["duration"]
	if ok {
		switch d := d.(type) {
		case time.Duration:
			duration = d
		case string:
			dur, err := time.ParseDuration(d)
			if err != nil {
				return nil, fmt.Errorf("Invalid duration: %s", err)
			}
			duration = dur
		}
	}

	return &cloudFrontStorageMiddleware{StorageDriver: storageDriver, cloudfront: cf, duration: duration}, nil
}

// S3BucketKeyer is any type that is capable of returning the S3 bucket key
// which should be cached by AWS CloudFront.
type S3BucketKeyer interface {
	S3BucketKey(path string) string
}

// Resolve returns an http.Handler which can serve the contents of the given
// Layer, or an error if not supported by the storagedriver.
func (lh *cloudFrontStorageMiddleware) URLFor(path string, options map[string]interface{}) (string, error) {
	// TODO(endophage): currently only supports S3
	keyer, ok := lh.StorageDriver.(S3BucketKeyer)
	if !ok {
		context.GetLogger(context.Background()).Warn("the CloudFront middleware does not support this backend storage driver")
		return lh.StorageDriver.URLFor(path, options)
	}

	cfURL, err := lh.cloudfront.CannedSignedURL(keyer.S3BucketKey(path), "", time.Now().Add(lh.duration))
	if err != nil {
		return "", err
	}
	return cfURL, nil
}

// init registers the cloudfront layerHandler backend.
func init() {
	storagemiddleware.Register("cloudfront", storagemiddleware.InitFunc(newCloudFrontStorageMiddleware))
}
