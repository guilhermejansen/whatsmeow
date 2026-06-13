// Copyright (c) 2025 Tulir Asokan
//
// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at http://mozilla.org/MPL/2.0/.

package types

import "time"

type OrderDetails struct {
	ID        string         `json:"id"`
	CreatedAt time.Time      `json:"created_at"`
	CatalogID string         `json:"catalog_id,omitempty"`
	Price     OrderPrice     `json:"price"`
	Products  []OrderProduct `json:"products"`
}

type OrderProduct struct {
	ID          string      `json:"id"`
	ImageID     string      `json:"image_id,omitempty"`
	ImageURL    string      `json:"image_url,omitempty"`
	Price       int64       `json:"price"`
	Currency    string      `json:"currency"`
	Name        string      `json:"name"`
	Quantity    int         `json:"quantity"`
	VariantInfo VariantInfo `json:"variant_info,omitempty"`
}

type VariantInfo struct {
	Properties string `json:"properties,omitempty"`
}

type OrderPrice struct {
	Subtotal    int64  `json:"subtotal"`
	Total       int64  `json:"total"`
	Currency    string `json:"currency"`
	PriceStatus string `json:"price_status,omitempty"`
}

type Catalog struct {
	ID          string    `json:"id"`
	BusinessJID JID       `json:"business_jid"`
	Products    []Product `json:"products"`
	NextCursor  *string   `json:"next_cursor,omitempty"`
}

type Product struct {
	ID           string   `json:"id"`
	RetailerID   string   `json:"retailer_id,omitempty"`
	Name         string   `json:"name"`
	Description  string   `json:"description,omitempty"`
	Price        int64    `json:"price"`
	Currency     string   `json:"currency"`
	ImageURLs    []string `json:"image_urls,omitempty"`
	Availability string   `json:"availability,omitempty"`
}

type Collection struct {
	ID         string   `json:"id"`
	Name       string   `json:"name"`
	ProductIDs []string `json:"product_ids"`
}
