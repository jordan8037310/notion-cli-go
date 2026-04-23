// This code is licensed under the Apache License, Version 2.0 (the "License").
// You may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://www.apache.org/licenses/LICENSE-2.0

package utils

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
)

// User is a Notion user object as returned by the /v1/users endpoints.
//
// Notion users come in two variants ("person" and "bot") distinguished by
// Type. The Person / Bot sub-objects are only populated for the matching
// variant; both are pointers so an omitted variant is clearly nil rather
// than a zero-value struct.
type User struct {
	Object    string      `json:"object"`
	ID        string      `json:"id"`
	Type      string      `json:"type,omitempty"`
	Name      string      `json:"name,omitempty"`
	AvatarURL string      `json:"avatar_url,omitempty"`
	Person    *UserPerson `json:"person,omitempty"`
	Bot       *UserBot    `json:"bot,omitempty"`
}

// UserPerson is the Notion person-variant sub-object.
type UserPerson struct {
	Email string `json:"email,omitempty"`
}

// UserBot is the Notion bot-variant sub-object. Owner identifies the
// integration or workspace that owns the bot.
type UserBot struct {
	Owner         *UserBotOwner `json:"owner,omitempty"`
	WorkspaceName string        `json:"workspace_name,omitempty"`
}

// UserBotOwner is the owner sub-object on a bot user.
type UserBotOwner struct {
	Type      string `json:"type,omitempty"`
	Workspace bool   `json:"workspace,omitempty"`
}

// UserList is a single page of users as returned by GET /v1/users.
type UserList struct {
	Object     string `json:"object"`
	Results    []User `json:"results"`
	NextCursor string `json:"next_cursor"`
	HasMore    bool   `json:"has_more"`
	Type       string `json:"type,omitempty"`
}

// UserClient is the typed resource client for the Notion users API.
type UserClient struct {
	c *Client
}

// NewUserClient wraps a *Client with user-resource methods.
func NewUserClient(c *Client) *UserClient {
	return &UserClient{c: c}
}

// ListPage fetches a single page of users. Pass an empty cursor for the
// first page; subsequent calls should pass the NextCursor returned by the
// previous page.
func (u *UserClient) ListPage(ctx context.Context, cursor string) (*UserList, error) {
	path := "/users"
	if cursor != "" {
		path += "?start_cursor=" + url.QueryEscape(cursor)
	}
	req, err := u.c.newRequest(ctx, http.MethodGet, path, nil)
	if err != nil {
		return nil, err
	}
	resp, err := u.c.do(req)
	if err != nil {
		return nil, fmt.Errorf("list users: %w", err)
	}
	var list UserList
	if err := decodeInto(resp, &list); err != nil {
		return nil, fmt.Errorf("list users: %w", err)
	}
	return &list, nil
}

// List fetches every user visible to the integration, walking pagination
// internally. Returns an empty slice (never nil) when the workspace has no
// users.
func (u *UserClient) List(ctx context.Context) ([]User, error) {
	results := make([]User, 0)
	cursor := ""
	for {
		page, err := u.ListPage(ctx, cursor)
		if err != nil {
			return nil, err
		}
		results = append(results, page.Results...)
		if !page.HasMore || page.NextCursor == "" {
			break
		}
		cursor = page.NextCursor
	}
	return results, nil
}

// Get retrieves a single user by id.
func (u *UserClient) Get(ctx context.Context, id string) (*User, error) {
	if id == "" {
		return nil, fmt.Errorf("get user: id is required")
	}
	req, err := u.c.newRequest(ctx, http.MethodGet, "/users/"+url.PathEscape(id), nil)
	if err != nil {
		return nil, err
	}
	resp, err := u.c.do(req)
	if err != nil {
		return nil, fmt.Errorf("get user: %w", err)
	}
	var user User
	if err := decodeInto(resp, &user); err != nil {
		return nil, fmt.Errorf("get user: %w", err)
	}
	return &user, nil
}

// Me returns the bot user associated with the integration token in use.
// Corresponds to GET /v1/users/me.
func (u *UserClient) Me(ctx context.Context) (*User, error) {
	req, err := u.c.newRequest(ctx, http.MethodGet, "/users/me", nil)
	if err != nil {
		return nil, err
	}
	resp, err := u.c.do(req)
	if err != nil {
		return nil, fmt.Errorf("get self: %w", err)
	}
	var user User
	if err := decodeInto(resp, &user); err != nil {
		return nil, fmt.Errorf("get self: %w", err)
	}
	return &user, nil
}
