/*
Adder API

Adder API

API version: v1
Contact: support@blinklabs.io
*/

// Code generated by OpenAPI Generator (https://openapi-generator.tech); DO NOT EDIT.

package openapi

import (
	"encoding/json"
)

// checks if the PushTokenResponse type satisfies the MappedNullable interface at compile time
var _ MappedNullable = &PushTokenResponse{}

// PushTokenResponse struct for PushTokenResponse
type PushTokenResponse struct {
	FcmToken *string `json:"fcmToken,omitempty"`
}

// NewPushTokenResponse instantiates a new PushTokenResponse object
// This constructor will assign default values to properties that have it defined,
// and makes sure properties required by API are set, but the set of arguments
// will change when the set of required properties is changed
func NewPushTokenResponse() *PushTokenResponse {
	this := PushTokenResponse{}
	return &this
}

// NewPushTokenResponseWithDefaults instantiates a new PushTokenResponse object
// This constructor will only assign default values to properties that have it defined,
// but it doesn't guarantee that properties required by API are set
func NewPushTokenResponseWithDefaults() *PushTokenResponse {
	this := PushTokenResponse{}
	return &this
}

// GetFcmToken returns the FcmToken field value if set, zero value otherwise.
func (o *PushTokenResponse) GetFcmToken() string {
	if o == nil || IsNil(o.FcmToken) {
		var ret string
		return ret
	}
	return *o.FcmToken
}

// GetFcmTokenOk returns a tuple with the FcmToken field value if set, nil otherwise
// and a boolean to check if the value has been set.
func (o *PushTokenResponse) GetFcmTokenOk() (*string, bool) {
	if o == nil || IsNil(o.FcmToken) {
		return nil, false
	}
	return o.FcmToken, true
}

// HasFcmToken returns a boolean if a field has been set.
func (o *PushTokenResponse) HasFcmToken() bool {
	if o != nil && !IsNil(o.FcmToken) {
		return true
	}

	return false
}

// SetFcmToken gets a reference to the given string and assigns it to the FcmToken field.
func (o *PushTokenResponse) SetFcmToken(v string) {
	o.FcmToken = &v
}

func (o PushTokenResponse) MarshalJSON() ([]byte, error) {
	toSerialize, err := o.ToMap()
	if err != nil {
		return []byte{}, err
	}
	return json.Marshal(toSerialize)
}

func (o PushTokenResponse) ToMap() (map[string]interface{}, error) {
	toSerialize := map[string]interface{}{}
	if !IsNil(o.FcmToken) {
		toSerialize["fcmToken"] = o.FcmToken
	}
	return toSerialize, nil
}

type NullablePushTokenResponse struct {
	value *PushTokenResponse
	isSet bool
}

func (v NullablePushTokenResponse) Get() *PushTokenResponse {
	return v.value
}

func (v *NullablePushTokenResponse) Set(val *PushTokenResponse) {
	v.value = val
	v.isSet = true
}

func (v NullablePushTokenResponse) IsSet() bool {
	return v.isSet
}

func (v *NullablePushTokenResponse) Unset() {
	v.value = nil
	v.isSet = false
}

func NewNullablePushTokenResponse(
	val *PushTokenResponse,
) *NullablePushTokenResponse {
	return &NullablePushTokenResponse{value: val, isSet: true}
}

func (v NullablePushTokenResponse) MarshalJSON() ([]byte, error) {
	return json.Marshal(v.value)
}

func (v *NullablePushTokenResponse) UnmarshalJSON(src []byte) error {
	v.isSet = true
	return json.Unmarshal(src, &v.value)
}
