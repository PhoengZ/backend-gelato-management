package cataloggrpc

import (
	pb "catalog-service/gen/catalog/v1"
	"catalog-service/internal/model"
	"catalog-service/internal/service"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
)

var allergenModels = map[pb.Allergen]model.Allergen{
	pb.Allergen_ALLERGEN_MILK:     model.AllergenMilk,
	pb.Allergen_ALLERGEN_EGG:      model.AllergenEgg,
	pb.Allergen_ALLERGEN_PEANUT:   model.AllergenPeanut,
	pb.Allergen_ALLERGEN_TREE_NUT: model.AllergenTreeNut,
	pb.Allergen_ALLERGEN_SOY:      model.AllergenSoy,
	pb.Allergen_ALLERGEN_GLUTEN:   model.AllergenGluten,
}

func inputFromProto(in *pb.FlavorInput) (service.CreateFlavorInput, error) {
	if in == nil {
		return service.CreateFlavorInput{}, invalidInput("flavor is required")
	}
	result := service.CreateFlavorInput{
		Name: in.Name, Description: in.Description, ImageURL: in.ImageUrl,
		Recipe: in.Recipe, Active: in.Active,
	}
	if in.Price != nil {
		if in.Price.AmountMinor == nil || in.Price.Currency == nil {
			return result, invalidInput("price requires amount_minor and currency")
		}
		result.Price = &model.Money{AmountMinor: *in.Price.AmountMinor, Currency: *in.Price.Currency}
	}
	if in.Allergens != nil {
		allergens := make([]model.Allergen, 0, len(in.Allergens.Values))
		for _, value := range in.Allergens.Values {
			mapped, ok := allergenModels[value]
			if !ok {
				return result, invalidInput("Unsupported allergen")
			}
			allergens = append(allergens, mapped)
		}
		result.Allergens = &allergens
	}
	return result, nil
}

func flavorToProto(in *model.Flavor) *pb.Flavor {
	out := &pb.Flavor{
		Id: in.ID.String(), Name: in.Name, Description: in.Description,
		Price:  &pb.Money{AmountMinor: proto.Int64(in.Price.AmountMinor), Currency: proto.String(in.Price.Currency)},
		Active: in.Active, CreatedAt: timestamppb.New(in.CreatedAt), UpdatedAt: timestamppb.New(in.UpdatedAt),
		Allergens: make([]pb.Allergen, 0, len(in.Allergens)),
	}
	if in.ImageURL != "" {
		out.ImageUrl = proto.String(in.ImageURL)
	}
	for _, value := range in.Allergens {
		out.Allergens = append(out.Allergens, pb.Allergen(pb.Allergen_value["ALLERGEN_"+string(value)]))
	}
	return out
}

func adminToProto(in *model.FlavorAdmin) *pb.FlavorAdmin {
	return &pb.FlavorAdmin{Flavor: flavorToProto(&in.Flavor), Recipe: in.Recipe}
}

func flavorsToProto(items []model.Flavor) []*pb.Flavor {
	out := make([]*pb.Flavor, 0, len(items))
	for i := range items {
		out = append(out, flavorToProto(&items[i]))
	}
	return out
}
