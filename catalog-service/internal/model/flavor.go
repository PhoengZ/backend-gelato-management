package model

import (
	"time"

	"github.com/google/uuid"
)

type Allergen string

const (
	AllergenMilk    Allergen = "MILK"
	AllergenEgg     Allergen = "EGG"
	AllergenPeanut  Allergen = "PEANUT"
	AllergenTreeNut Allergen = "TREE_NUT"
	AllergenSoy     Allergen = "SOY"
	AllergenGluten  Allergen = "GLUTEN"
)

func (a Allergen) Valid() bool {
	switch a {
	case AllergenMilk, AllergenEgg, AllergenPeanut, AllergenTreeNut, AllergenSoy, AllergenGluten:
		return true
	default:
		return false
	}
}

type Money struct {
	AmountMinor int64  `json:"amount_minor"`
	Currency    string `json:"currency"`
}

type Flavor struct {
	ID          uuid.UUID  `json:"id"`
	Name        string     `json:"name"`
	Description string     `json:"description"`
	Price       Money      `json:"price"`
	ImageURL    string     `json:"image_url,omitempty"`
	Allergens   []Allergen `json:"allergens"`
	Active      bool       `json:"active"`
	CreatedAt   time.Time  `json:"created_at"`
	UpdatedAt   time.Time  `json:"updated_at"`
}

type FlavorAdmin struct {
	Flavor
	Recipe string `json:"recipe"`
}

type FlavorRecipe struct {
	FlavorID  uuid.UUID `json:"flavor_id"`
	Recipe    string    `json:"recipe"`
	UpdatedAt time.Time `json:"updated_at"`
}
