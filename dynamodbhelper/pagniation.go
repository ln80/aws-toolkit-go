package dynamodbhelper

import (
	"encoding/base64"
	"encoding/json"
	"strings"
	"unicode"

	"github.com/aws/aws-sdk-go-v2/feature/dynamodb/attributevalue"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	"golang.org/x/text/runes"
	"golang.org/x/text/unicode/norm"
)

func NormalizeText(s string) string {
	s = strings.ToLowerSpecial(unicode.TurkishCase, s)
	s = norm.NFD.String(s)
	s = runes.Remove(runes.In(unicode.Mn)).String(s)

	return s
}

type PagingKey map[string]types.AttributeValue

func EncodePagingKey[T any](key PagingKey) (*string, error) {
	if key == nil {
		return nil, nil
	}

	var r T
	// r := MainItem{}
	if err := attributevalue.UnmarshalMap(key, &r); err != nil {
		return nil, err
	}
	bytes, err := json.Marshal(&r)
	if err != nil {
		return nil, err
	}

	encodedKey := base64.StdEncoding.EncodeToString(bytes)
	return &encodedKey, nil
}

func DecodePagingKey[T any](encodedKey string) (PagingKey, error) {
	if encodedKey == "" {
		return nil, nil
	}

	sKey, err := base64.StdEncoding.DecodeString(encodedKey)
	if err != nil {
		return nil, err
	}

	var r T
	// r := MainItem{}
	if err = json.Unmarshal(sKey, &r); err != nil {
		return nil, err
	}
	key, err := attributevalue.MarshalMap(r)
	if err != nil {
		return nil, err
	}

	return key, nil
}
