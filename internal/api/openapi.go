package api

import (
	"fmt"
	"net/http"
	"reflect"
	"sort"
	"strings"
	"unicode"

	apiHandlers "github.com/chargeghost/engine/internal/api/handlers"
	"github.com/chargeghost/engine/internal/config"
	engine "github.com/chargeghost/engine/internal/engine"
	"github.com/chargeghost/engine/internal/ocpp"
	"github.com/go-chi/chi/v5"
	openapi "github.com/swaggest/openapi-go"
	"github.com/swaggest/openapi-go/openapi31"
	swgui "github.com/swaggest/swgui/v5"
)

const (
	openAPIDocumentPath = "/openapi.json"
	openAPIYAMLPath     = "/openapi.yaml"
	swaggerUIPath       = "/swagger"
)

// mountOpenAPIRoutes creates the OpenAPI document from the routes currently
// registered on r, then exposes it alongside the embedded Swagger UI.
func mountOpenAPIRoutes(r chi.Router, source chi.Routes) {
	document, err := buildOpenAPIDocument(source)
	if err != nil {
		panic(fmt.Sprintf("build OpenAPI document: %v", err))
	}

	r.Get(openAPIDocumentPath, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(document.json)
	})
	r.Get(openAPIYAMLPath, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/yaml")
		_, _ = w.Write(document.yaml)
	})
	r.Mount(swaggerUIPath, swgui.New("ChargeGhost REST API", openAPIDocumentPath, swaggerUIPath))
}

func openAPIRouteTree() chi.Router {
	r := chi.NewRouter()
	r.Get("/health", http.NotFound)
	r.Route("/api/v1", func(r chi.Router) {
		app := &AppContext{}
		mountStationRoutesForOpenAPI(r, app, false)
		mountFleetRoutes(r, nil)

		station := chi.NewRouter()
		mountStationRoutesForOpenAPI(station, app, true)
		mountFleetStationRoutesAuth(station, nil, "")
		r.Mount("/stations/{station_id}", station)
	})
	r.Get("/ws", http.NotFound)
	return r
}

type openAPIDocument struct {
	json []byte
	yaml []byte
}

func buildOpenAPIDocument(r chi.Routes) (openAPIDocument, error) {
	reflector := openapi31.NewReflector()
	reflector.Spec.Info.WithTitle("ChargeGhost REST API").
		WithVersion("1.0.0").
		WithDescription("REST API for the ChargeGhost EVSE simulation engine.")

	type route struct {
		method string
		path   string
	}
	routes := make([]route, 0)
	seen := make(map[string]struct{})

	err := chi.Walk(r, func(method, path string, _ http.Handler, _ ...func(http.Handler) http.Handler) error {
		if !isOpenAPIMethod(method) {
			return nil
		}
		path = openAPIPath(path)
		key := method + " " + path
		if _, ok := seen[key]; ok {
			return nil
		}
		seen[key] = struct{}{}
		routes = append(routes, route{method: method, path: path})
		return nil
	})
	if err != nil {
		return openAPIDocument{}, err
	}

	sort.Slice(routes, func(i, j int) bool {
		if routes[i].path == routes[j].path {
			return routes[i].method < routes[j].method
		}
		return routes[i].path < routes[j].path
	})

	for _, route := range routes {
		operation, err := reflector.NewOperationContext(route.method, route.path)
		if err != nil {
			return openAPIDocument{}, err
		}

		operation.SetID(operationID(route.method, route.path))
		operation.SetSummary(strings.ToUpper(route.method) + " " + route.path)
		operation.SetTags(routeTag(route.path))
		if params, ok := pathParameters(route.path); ok {
			operation.AddReqStructure(params)
		}
		schema := schemaForRoute(route.method, route.path)
		if schema.request != nil {
			operation.AddReqStructure(schema.request, openapi.WithContentType("application/json"))
		}
		response := schema.response
		if response == nil {
			response = new(map[string]interface{})
		}
		operation.AddRespStructure(response, openapi.WithHTTPStatus(schema.responseStatus))

		if err := reflector.AddOperation(operation); err != nil {
			return openAPIDocument{}, err
		}
	}

	jsonDocument, err := reflector.Spec.MarshalJSON()
	if err != nil {
		return openAPIDocument{}, err
	}
	yamlDocument, err := reflector.Spec.MarshalYAML()
	if err != nil {
		return openAPIDocument{}, err
	}

	return openAPIDocument{json: jsonDocument, yaml: yamlDocument}, nil
}

type operationSchema struct {
	request        interface{}
	response       interface{}
	responseStatus int
}

func schemaForRoute(method, path string) operationSchema {
	method = strings.ToUpper(method)
	path = strings.TrimSuffix(path, "/")
	if path == "" {
		path = "/"
	}

	if schema, ok := stationRouteSchema(method, path); ok {
		return schema
	}

	key := method + " " + path
	schema := operationSchema{responseStatus: http.StatusOK}
	switch key {
	case "GET /api/v1/status":
		schema.response = apiHandlers.StatusResponseDTO{}
	case "GET /api/v1/connectors":
		schema.response = []apiHandlers.ConnectorDTO{}
	case "POST /api/v1/connectors":
		schema.request = CreateConnectorRequest{}
		schema.responseStatus = http.StatusCreated
	case "GET /api/v1/connectors/{id}":
		schema.response = apiHandlers.ConnectorDTO{}
	case "PUT /api/v1/connectors/{id}":
		schema.request = UpdateConnectorRequest{}
	case "PUT /api/v1/connectors/{id}/availability":
		schema.request = UpdateAvailabilityRequest{}
	case "GET /api/v1/sessions":
		schema.response = []apiHandlers.SessionDTO{}
	case "POST /api/v1/sessions/start":
		schema.request = StartSessionRequest{}
	case "GET /api/v1/sessions/last-stopped":
		schema.response = StoppedSessionDTO{}
	case "GET /api/v1/sessions/active", "GET /api/v1/sessions/{connector_id}":
		schema.response = apiHandlers.SessionDTO{}
	case "GET /api/v1/sessions/info":
		schema.response = []apiHandlers.SessionDTO{}
	case "GET /api/v1/config":
		schema.response = config.Config{}
	case "PATCH /api/v1/config":
		schema.request = PatchConfigRequest{}
		schema.response = PatchConfigResponse{}
	case "GET /api/v1/reservations":
		schema.response = []ReservationDTO{}
	case "POST /api/v1/reservations":
		schema.request = CreateReservationRequest{}
		schema.responseStatus = http.StatusCreated
	case "GET /api/v1/timeline":
		schema.response = apiHandlers.TimelineResponse{}
	case "GET /api/v1/timeline/count":
		schema.response = apiHandlers.TimelineCountResponse{}
	case "GET /api/v1/local-auth-list":
		schema.response = apiHandlers.LocalAuthListResponse{}
	case "GET /api/v1/local-auth-list/{id_tag}":
		schema.response = apiHandlers.LocalAuthEntryDTO{}
	case "PUT /api/v1/local-auth-list":
		schema.request = apiHandlers.UpdateLocalAuthListRequest{}
		schema.response = apiHandlers.UpdateLocalAuthListResponse{}
	case "GET /api/v1/firmware/status":
		schema.response = ocpp.FirmwareStatus{}
	case "POST /api/v1/firmware/trigger":
		schema.request = apiHandlers.FirmwareUpdateRequest{}
	case "GET /api/v1/diagnostics/status":
		schema.response = ocpp.DiagnosticsStatus{}
	case "POST /api/v1/diagnostics/trigger":
		schema.request = apiHandlers.DiagnosticsUploadRequest{}
	case "GET /api/v1/charging-profiles":
		schema.response = []engine.ChargingProfile{}
	case "GET /api/v1/charging-profiles/{profile_id}":
		schema.response = engine.ChargingProfile{}
	case "POST /api/v1/charging-profiles":
		schema.request = apiHandlers.InstallChargingProfileRequest{}
	case "POST /api/v1/charging-profiles/composite-schedule":
		schema.request = apiHandlers.CompositeScheduleRequest{}
		schema.response = apiHandlers.CompositeScheduleResponse{}
	case "GET /api/v1/ocpp/config-keys":
		schema.response = []ocpp.ConfigKeyEntry{}
	case "PATCH /api/v1/ocpp/config-keys":
		schema.request = apiHandlers.PatchOCPPConfigKeyRequest{}
	case "POST /api/v1/ocpp/authorize":
		schema.request = apiHandlers.AuthorizeRequest{}
	case "POST /api/v1/ocpp/raw/status-notification":
		schema.request = apiHandlers.RawStatusNotificationRequest{}
	case "POST /api/v1/ocpp/raw/meter-values":
		schema.request = apiHandlers.RawMeterValuesRequest{}
	case "POST /api/v1/ocpp/raw/data-transfer":
		schema.request = apiHandlers.RawDataTransferRequest{}
		schema.response = apiHandlers.DataTransferResponse{}
	case "POST /api/v1/ocpp/raw/start-transaction":
		schema.request = apiHandlers.RawStartTransactionRequest{}
	case "POST /api/v1/ocpp/raw/stop-transaction":
		schema.request = apiHandlers.RawStopTransactionRequest{}
	case "GET /api/v1/about":
		schema.response = apiHandlers.AboutResponse{}
	}
	return schema
}

func stationRouteSchema(method, path string) (operationSchema, bool) {
	prefix := "/api/v1/stations/{station_id}"
	if !strings.HasPrefix(path, prefix) {
		return operationSchema{}, false
	}
	suffix := strings.TrimPrefix(path, prefix)
	if suffix == "" {
		suffix = "/"
	}

	schema := operationSchema{responseStatus: http.StatusOK}
	switch method + " " + suffix {
	case "GET /status":
		schema.response = StationSnapshot{}
	case "PATCH /config":
		schema.request = PatchStationConfigRequest{}
		schema.response = PatchStationResponse{}
	case "DELETE /":
	case "POST /start", "POST /stop", "POST /restart", "POST /enable", "POST /disable", "POST /reload", "POST /persist", "POST /ocpp/reconnect":
		schema.response = OperationResponse{}
		schema.responseStatus = http.StatusAccepted
	case "PUT /credentials/ocpp-password":
		schema.request = SetOCPPPasswordRequest{}
	case "GET /queue/status":
		schema.response = QueueStatus{}
	case "POST /queue/drain":
		schema.response = OperationResponse{}
		schema.responseStatus = http.StatusAccepted
	case "GET /queue/dead-letter":
		schema.response = []DeadLetterEntry{}
	default:
		// Operational station routes share the default station schemas.
		if strings.HasPrefix(suffix, "/connectors") || strings.HasPrefix(suffix, "/sessions") ||
			strings.HasPrefix(suffix, "/config") || strings.HasPrefix(suffix, "/reservations") ||
			strings.HasPrefix(suffix, "/timeline") || strings.HasPrefix(suffix, "/local-auth-list") ||
			strings.HasPrefix(suffix, "/firmware") || strings.HasPrefix(suffix, "/diagnostics") ||
			strings.HasPrefix(suffix, "/charging-profiles") || strings.HasPrefix(suffix, "/ocpp") ||
			suffix == "/about" {
			return schemaForRoute(method, "/api/v1"+suffix), true
		}
		return operationSchema{}, false
	}
	return schema, true
}

func isOpenAPIMethod(method string) bool {
	switch strings.ToUpper(method) {
	case http.MethodGet, http.MethodPut, http.MethodPost, http.MethodDelete,
		http.MethodOptions, http.MethodHead, http.MethodPatch, http.MethodTrace:
		return true
	default:
		return false
	}
}

func openAPIPath(path string) string {
	parts := strings.Split(path, "/")
	for i, part := range parts {
		switch {
		case part == "*":
			parts[i] = "{path}"
		case strings.HasPrefix(part, "{") && strings.HasSuffix(part, "}"):
			name := strings.TrimSuffix(strings.TrimPrefix(part, "{"), "}")
			parts[i] = "{" + strings.SplitN(name, ":", 2)[0] + "}"
		}
	}
	return strings.Join(parts, "/")
}

func pathParameters(path string) (interface{}, bool) {
	var fields []reflect.StructField
	for _, part := range strings.Split(path, "/") {
		if !strings.HasPrefix(part, "{") || !strings.HasSuffix(part, "}") {
			continue
		}

		name := strings.TrimSuffix(strings.TrimPrefix(part, "{"), "}")
		if name == "" {
			continue
		}

		fieldName := "Path" + strings.Map(func(r rune) rune {
			if unicode.IsLetter(r) || unicode.IsDigit(r) {
				return r
			}
			return -1
		}, name)
		fields = append(fields, reflect.StructField{
			Name: fieldName,
			Type: reflect.TypeOf(""),
			Tag:  reflect.StructTag(`path:"` + name + `"`),
		})
	}

	if len(fields) == 0 {
		return nil, false
	}
	return reflect.New(reflect.StructOf(fields)).Interface(), true
}

func operationID(method, path string) string {
	var b strings.Builder
	b.WriteString(strings.ToLower(method))
	for _, r := range path {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(r)
			continue
		}
		if b.Len() > 0 && b.String()[b.Len()-1] != '_' {
			b.WriteByte('_')
		}
	}
	return strings.Trim(b.String(), "_")
}

func routeTag(path string) string {
	parts := strings.Split(strings.Trim(path, "/"), "/")
	for _, part := range parts {
		if part != "" && !strings.HasPrefix(part, "{") {
			return part
		}
	}
	return "system"
}
