# \DefaultAPI

All paths below append to the configured server URL. Use an absolute URL ending
in `/v1`. These methods exist in [api_default.go](../api_default.go); their
string return types reflect the generated client, while current POST/DELETE
server successes have empty bodies. FCM routes require the push output.

Method | HTTP request | Description
------------- | ------------- | -------------
[**FcmPost**](DefaultAPI.md#fcmpost) | **Post** /fcm | Store FCM Token
[**FcmTokenDelete**](DefaultAPI.md#fcmtokendelete) | **Delete** /fcm/{token} | Delete FCM Token
[**FcmTokenGet**](DefaultAPI.md#fcmtokenget) | **Get** /fcm/{token} | Get FCM Token



## FcmPost

> string FcmPost(ctx).Body(body).Execute()

Store FCM Token



### Example

```go
package main

import (
	"context"
	"fmt"
	"os"
	openapiclient "github.com/blinklabs-io/cardano-node-api/openapi"
)

func main() {
	body := *openapiclient.NewPushTokenRequest("FcmToken_example") // PushTokenRequest | FCM Token Request

	configuration := openapiclient.NewConfiguration()
	configuration.Servers = openapiclient.ServerConfigurations{
		{URL: "http://127.0.0.1:8080/v1"},
	}
	apiClient := openapiclient.NewAPIClient(configuration)
	resp, r, err := apiClient.DefaultAPI.FcmPost(context.Background()).Body(body).Execute()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error when calling `DefaultAPI.FcmPost``: %v\n", err)
		fmt.Fprintf(os.Stderr, "Full HTTP response: %v\n", r)
	}
	// response from `FcmPost`: string
	fmt.Fprintf(os.Stdout, "Response from `DefaultAPI.FcmPost`: %v\n", resp)
}
```

### Path Parameters



### Other Parameters

Other parameters are passed through a pointer to a apiFcmPostRequest struct via the builder pattern


Name | Type | Description  | Notes
------------- | ------------- | ------------- | -------------
 **body** | [**PushTokenRequest**](PushTokenRequest.md) | FCM Token Request | 

### Return type

**string** (empty for current successful server responses; inspect HTTP status)

### Authorization

No authorization required

### HTTP request headers

- **Content-Type**: application/json
- **Accept**: application/json

[[Back to top]](#) [[Back to API list]](../README.md#documentation-for-api-endpoints)
[[Back to Model list]](../README.md#documentation-for-models)
[[Back to README]](../README.md)


## FcmTokenDelete

> string FcmTokenDelete(ctx, token).Execute()

Delete FCM Token



### Example

```go
package main

import (
	"context"
	"fmt"
	"os"
	openapiclient "github.com/blinklabs-io/cardano-node-api/openapi"
)

func main() {
	token := "token_example" // string | FCM Token

	configuration := openapiclient.NewConfiguration()
	configuration.Servers = openapiclient.ServerConfigurations{
		{URL: "http://127.0.0.1:8080/v1"},
	}
	apiClient := openapiclient.NewAPIClient(configuration)
	resp, r, err := apiClient.DefaultAPI.FcmTokenDelete(context.Background(), token).Execute()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error when calling `DefaultAPI.FcmTokenDelete``: %v\n", err)
		fmt.Fprintf(os.Stderr, "Full HTTP response: %v\n", r)
	}
	// response from `FcmTokenDelete`: string
	fmt.Fprintf(os.Stdout, "Response from `DefaultAPI.FcmTokenDelete`: %v\n", resp)
}
```

### Path Parameters


Name | Type | Description  | Notes
------------- | ------------- | ------------- | -------------
**ctx** | **context.Context** | context for authentication, logging, cancellation, deadlines, tracing, etc.
**token** | **string** | FCM Token | 

### Other Parameters

Other parameters are passed through a pointer to a apiFcmTokenDeleteRequest struct via the builder pattern


Name | Type | Description  | Notes
------------- | ------------- | ------------- | -------------


### Return type

**string** (empty for current successful server responses; inspect HTTP status)

### Authorization

No authorization required

### HTTP request headers

- **Content-Type**: Not defined
- **Accept**: application/json

[[Back to top]](#) [[Back to API list]](../README.md#documentation-for-api-endpoints)
[[Back to Model list]](../README.md#documentation-for-models)
[[Back to README]](../README.md)


## FcmTokenGet

> PushTokenResponse FcmTokenGet(ctx, token).Execute()

Get FCM Token



### Example

```go
package main

import (
	"context"
	"fmt"
	"os"
	openapiclient "github.com/blinklabs-io/cardano-node-api/openapi"
)

func main() {
	token := "token_example" // string | FCM Token

	configuration := openapiclient.NewConfiguration()
	configuration.Servers = openapiclient.ServerConfigurations{
		{URL: "http://127.0.0.1:8080/v1"},
	}
	apiClient := openapiclient.NewAPIClient(configuration)
	resp, r, err := apiClient.DefaultAPI.FcmTokenGet(context.Background(), token).Execute()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error when calling `DefaultAPI.FcmTokenGet``: %v\n", err)
		fmt.Fprintf(os.Stderr, "Full HTTP response: %v\n", r)
	}
	// response from `FcmTokenGet`: PushTokenResponse
	fmt.Fprintf(os.Stdout, "Response from `DefaultAPI.FcmTokenGet`: %v\n", resp)
}
```

### Path Parameters


Name | Type | Description  | Notes
------------- | ------------- | ------------- | -------------
**ctx** | **context.Context** | context for authentication, logging, cancellation, deadlines, tracing, etc.
**token** | **string** | FCM Token | 

### Other Parameters

Other parameters are passed through a pointer to a apiFcmTokenGetRequest struct via the builder pattern


Name | Type | Description  | Notes
------------- | ------------- | ------------- | -------------


### Return type

[**PushTokenResponse**](PushTokenResponse.md)

### Authorization

No authorization required

### HTTP request headers

- **Content-Type**: Not defined
- **Accept**: application/json

[[Back to top]](#) [[Back to API list]](../README.md#documentation-for-api-endpoints)
[[Back to Model list]](../README.md#documentation-for-models)
[[Back to README]](../README.md)

