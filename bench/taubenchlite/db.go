package main

type worldState struct {
	Users   map[string]userRow   `json:"users"`
	Orders  map[string]orderRow  `json:"orders"`
	Refunds map[string]refundRow `json:"refunds"`
}

type userRow struct {
	Name  string `json:"name"`
	Email string `json:"email"`
}

type orderRow struct {
	UserID  string  `json:"userId"`
	Status  string  `json:"status"`
	Address string  `json:"address"`
	Item    string  `json:"item"`
	Price   float64 `json:"price"`
}

type refundRow struct {
	OrderID string  `json:"orderId"`
	Reason  string  `json:"reason"`
	Amount  float64 `json:"amount"`
}

func retailSeed() worldState {
	return worldState{
		Users: map[string]userRow{
			"u_ari": {Name: "Ari Chen", Email: "ari@example.com"},
			"u_bo":  {Name: "Bo Wang", Email: "bo@example.com"},
			"u_cai": {Name: "Cai Lin", Email: "cai@example.com"},
			"u_dev": {Name: "Dev Patel", Email: "dev@example.com"},
		},
		Orders: map[string]orderRow{
			"o_1001": {
				UserID:  "u_ari",
				Status:  "shipped",
				Address: "1 Elm St, SF, CA 94110",
				Item:    "wool sweater M",
				Price:   89.0,
			},
			"o_1002": {
				UserID:  "u_bo",
				Status:  "processing",
				Address: "22 Oak Rd, NYC, NY 10001",
				Item:    "running shoes 10",
				Price:   140.0,
			},
			"o_1003": {
				UserID:  "u_cai",
				Status:  "delivered",
				Address: "9 Pine Ave, Austin, TX 78701",
				Item:    "desk lamp",
				Price:   55.0,
			},
			"o_1004": {
				UserID:  "u_dev",
				Status:  "processing",
				Address: "4 Maple Ln, Seattle, WA 98101",
				Item:    "kettle",
				Price:   45.0,
			},
			"o_1005": {
				UserID:  "u_ari",
				Status:  "delivered",
				Address: "1 Elm St, SF, CA 94110",
				Item:    "notebook pack",
				Price:   22.0,
			},
		},
		Refunds: map[string]refundRow{},
	}
}

func cloneDB(db worldState) worldState {
	out := worldState{
		Users:   make(map[string]userRow, len(db.Users)),
		Orders:  make(map[string]orderRow, len(db.Orders)),
		Refunds: make(map[string]refundRow, len(db.Refunds)),
	}
	for k, v := range db.Users {
		out.Users[k] = v
	}
	for k, v := range db.Orders {
		out.Orders[k] = v
	}
	for k, v := range db.Refunds {
		out.Refunds[k] = v
	}
	return out
}
