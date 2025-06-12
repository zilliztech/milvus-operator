package external

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"net/http"
	"strings"
	"time"

	madmin "github.com/minio/madmin-go"
	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"

	"github.com/zilliztech/milvus-operator/apis/milvus.io/v1beta1"
	"github.com/zilliztech/milvus-operator/pkg/util"
)

// CheckMinIOArgs is info for acquiring storage condition
type CheckMinIOArgs struct {
	// S3 / MinIO
	Type           string
	AK             string
	SK             string
	Bucket         string
	Endpoint       string
	UseSSL         bool
	UseIAM         bool
	IAMEndpoint    string
	UseVirtualHost bool
	// SSL Configuration
	CACertificate      []byte
	InsecureSkipVerify bool
}

var DependencyCheckTimeout = 5 * time.Second

func CheckMinIO(args CheckMinIOArgs) error {
	var checkMinio = func() error {
		ctx, cancel := context.WithTimeout(context.Background(), DependencyCheckTimeout)
		defer cancel()

		switch args.Type {
		case v1beta1.StorageTypeS3:
			endpoint := args.Endpoint
			if args.UseSSL {
				// minio client cannot recognize aws endpoints with :443
				endpoint = strings.TrimSuffix(endpoint, ":443")
			}
			bucketLookup := minio.BucketLookupPath
			if args.UseVirtualHost {
				bucketLookup = minio.BucketLookupDNS
			}
			
			options := &minio.Options{
				// GetBucketLocation will succeed as long as the bucket exists
				Creds:        credentials.NewStaticV4(args.AK, args.SK, ""),
				Secure:       args.UseSSL,
				BucketLookup: bucketLookup,
			}
			
			// Configure custom TLS if SSL is enabled and custom configuration is provided
			if args.UseSSL && (len(args.CACertificate) > 0 || args.InsecureSkipVerify) {
				tlsConfig := &tls.Config{
					InsecureSkipVerify: args.InsecureSkipVerify,
				}
				
				// Add custom CA certificate if provided
				if len(args.CACertificate) > 0 {
					caCertPool := x509.NewCertPool()
					if !caCertPool.AppendCertsFromPEM(args.CACertificate) {
						return errors.New("failed to parse CA certificate")
					}
					tlsConfig.RootCAs = caCertPool
				}
				
				transport := &http.Transport{
					TLSClientConfig: tlsConfig,
				}
				options.Transport = transport
			}
			
			cli, err := minio.New(endpoint, options)
			if err != nil {
				return err
			}
			// see cli.HealthCheck()
			// there will be 43k requests per month (1 per minute)
			// will charge extra $0.02 per month by aws
			// according to https://aws.amazon.com/s3/pricing/
			_, err = cli.GetBucketLocation(ctx, args.Bucket)
			return err
		case v1beta1.StorageTypeAzure:
			// azure storage uses a compleletly different set of api. we simply assume ok to support azure for now
			if args.AK == "" {
				return errors.New("azure storage account name is required to be set as minio.accessKeyID")
			}
			return nil
		default:
			// default to minio
			// Create MinIO admin client with SSL configuration
			mcli, err := madmin.New(args.Endpoint, args.AK, args.SK, args.UseSSL)
			if err != nil {
				return err
			}
			
			// Configure custom TLS if SSL is enabled and custom configuration is provided
			if args.UseSSL && (len(args.CACertificate) > 0 || args.InsecureSkipVerify) {
				tlsConfig := &tls.Config{
					InsecureSkipVerify: args.InsecureSkipVerify,
				}
				
				// Add custom CA certificate if provided
				if len(args.CACertificate) > 0 {
					caCertPool := x509.NewCertPool()
					if !caCertPool.AppendCertsFromPEM(args.CACertificate) {
						return errors.New("failed to parse CA certificate")
					}
					tlsConfig.RootCAs = caCertPool
				}
				
				transport := &http.Transport{
					TLSClientConfig: tlsConfig,
				}
				mcli.SetCustomTransport(transport)
			}
			
			st, err := mcli.ServerInfo(ctx)
			if err != nil {
				return err
			}
			return isHealthyByServerInfo(st)
		}
	}
	return util.DoWithBackoff("checkMinIO", checkMinio, util.DefaultMaxRetry, util.DefaultBackOffInterval)
}

func isHealthyByServerInfo(st madmin.InfoMessage) error {
	for _, server := range st.Servers {
		if server.State == "ok" || server.State == "online" {
			return nil
		}
	}
	return errors.New("no server ready in server info")
}
