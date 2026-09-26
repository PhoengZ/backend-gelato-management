package cataloggrpc

import (
	"context"

	pb "catalog-service/gen/catalog/v1"
	"catalog-service/internal/service"

	"github.com/google/uuid"
)

type handler struct {
	pb.UnimplementedCatalogServiceServer
	svc service.CatalogService
}

func (h *handler) CreateFlavor(ctx context.Context, req *pb.CreateFlavorRequest) (*pb.CreateFlavorResponse, error) {
	input, err := inputFromProto(req.GetFlavor())
	if err != nil {
		return nil, err
	}
	result, err := h.svc.Create(ctx, input)
	if err != nil {
		return nil, serviceError(err)
	}
	return &pb.CreateFlavorResponse{Flavor: adminToProto(result)}, nil
}

func (h *handler) GetFlavor(ctx context.Context, req *pb.GetFlavorRequest) (*pb.GetFlavorResponse, error) {
	id, err := parseID(req.GetFlavorId())
	if err != nil {
		return nil, err
	}
	result, err := h.svc.Get(ctx, id, readScope(ctx))
	if err != nil {
		return nil, serviceError(err)
	}
	return &pb.GetFlavorResponse{Flavor: flavorToProto(result)}, nil
}

func (h *handler) ListFlavors(ctx context.Context, req *pb.ListFlavorsRequest) (*pb.ListFlavorsResponse, error) {
	if req.Active != nil && !*req.Active {
		if err := requireManager(ctx); err != nil {
			return nil, err
		}
	}
	result, err := h.svc.List(ctx, req.Active, readScope(ctx))
	if err != nil {
		return nil, serviceError(err)
	}
	return &pb.ListFlavorsResponse{Items: flavorsToProto(result)}, nil
}

func (h *handler) BatchGetFlavors(ctx context.Context, req *pb.BatchGetFlavorsRequest) (*pb.BatchGetFlavorsResponse, error) {
	if len(req.GetFlavorIds()) < 1 || len(req.GetFlavorIds()) > 100 {
		return nil, invalidInput("Between 1 and 100 flavor IDs are required")
	}
	ids := make([]uuid.UUID, 0, len(req.FlavorIds))
	for _, raw := range req.FlavorIds {
		id, err := parseID(raw)
		if err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	result, err := h.svc.BatchGet(ctx, ids, readScope(ctx))
	if err != nil {
		return nil, serviceError(err)
	}
	return &pb.BatchGetFlavorsResponse{Items: flavorsToProto(result)}, nil
}

func (h *handler) UpdateFlavor(ctx context.Context, req *pb.UpdateFlavorRequest) (*pb.UpdateFlavorResponse, error) {
	id, err := parseID(req.GetFlavorId())
	if err != nil {
		return nil, err
	}
	input, err := inputFromProto(req.GetFlavor())
	if err != nil {
		return nil, err
	}
	result, err := h.svc.Replace(ctx, id, service.ReplaceFlavorInput(input))
	if err != nil {
		return nil, serviceError(err)
	}
	return &pb.UpdateFlavorResponse{Flavor: adminToProto(result)}, nil
}

func (h *handler) DeleteFlavor(ctx context.Context, req *pb.DeleteFlavorRequest) (*pb.DeleteFlavorResponse, error) {
	id, err := parseID(req.GetFlavorId())
	if err != nil {
		return nil, err
	}
	if err := h.svc.Archive(ctx, id); err != nil {
		return nil, serviceError(err)
	}
	return &pb.DeleteFlavorResponse{}, nil
}

func parseID(raw string) (uuid.UUID, error) {
	id, err := uuid.Parse(raw)
	if err != nil {
		return uuid.Nil, invalidInput("flavor_id must be a UUID")
	}
	return id, nil
}
