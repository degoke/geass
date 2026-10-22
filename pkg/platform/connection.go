package platform

const (
	ConnectionKeyHost      = "host"
	ConnectionKeyPort      = "port"
	ConnectionKeyURI       = "uri"
	ConnectionKeyPassword  = "password"
	ConnectionKeyDatabase  = "database"
	ConnectionKeyUsername  = "username"
	ConnectionKeyAccessKey = "accessKey"
	ConnectionKeySecretKey = "secretKey"
	ConnectionKeyEndpoint  = "endpoint"
	ConnectionKeyBucket    = "bucket"
	ConnectionKeyRegion    = "region"
	PostgresDefaultPort    = "5432"
	MySQLDefaultPort       = "3306"
	SQLiteDefaultPort      = "4001"
	RedisDefaultPort       = "6379"

	SecretKeyAccessKeyID     = "accessKeyId"
	SecretKeySecretAccessKey = "secretAccessKey"
	SecretKeyRegion          = "region"
	SecretKeyOrganization    = "organization"
	// SecretKeySessionToken is the third part of temporary AWS credentials (from sts:AssumeRole). Only a connection for
	// machines reads it: the Amazon EC2 driver sends and signs it with every request. It is optional.
	SecretKeySessionToken = "sessionToken"
)

const (
	SecretKeyToken    = "token"
	SecretKeyValue    = "value"
	SecretKeyK3sToken = "k3s-token"
)
