# PushTokenRequest

## Properties

Name | Type | Description | Notes
------------ | ------------- | ------------- | -------------
**FcmToken** | **string** |  | 

## Methods

### NewPushTokenRequest

`func NewPushTokenRequest(fcmToken string) *PushTokenRequest`

Stores the supplied string. It does not reject an empty token; the server does.

### NewPushTokenRequestWithDefaults

`func NewPushTokenRequestWithDefaults() *PushTokenRequest`

NewPushTokenRequestWithDefaults instantiates a new PushTokenRequest object
This constructor will only assign default values to properties that have it defined,
but it doesn't guarantee that properties required by API are set

### GetFcmToken

`func (o *PushTokenRequest) GetFcmToken() string`

Returns the token string, or an empty string for a nil receiver.

### GetFcmTokenOk

`func (o *PushTokenRequest) GetFcmTokenOk() (*string, bool)`

Returns a pointer to the string and true for any non-nil receiver, even if the
string is empty. A nil receiver returns nil and false.

### SetFcmToken

`func (o *PushTokenRequest) SetFcmToken(v string)`

SetFcmToken sets FcmToken field to given value.



[[Back to Model list]](../README.md#documentation-for-models) [[Back to API list]](../README.md#documentation-for-api-endpoints) [[Back to README]](../README.md)


