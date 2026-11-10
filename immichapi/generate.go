// Package immichapi is a client for the Immich server API, generated from
// the server's OpenAPI specification.
//
// The specification is a snapshot of
// https://github.com/immich-app/immich/blob/main/open-api/immich-openapi-specs.json
// at commit 173bb5a2cd67731657ba975c554c744b2a71039d. Its paths are relative
// to the server's /api prefix, so a client takes the server's address with
// that prefix:
//
//	client, err := immichapi.NewClientWithResponses(server + "/api")
//	resp, err := client.PingServerWithResponse(ctx)
//
// Regenerate the client with:
//
//	go generate ./immichapi
package immichapi

//go:generate go tool oapi-codegen -config oapi-codegen.yaml ../openapi/immich-openapi-specs.json
