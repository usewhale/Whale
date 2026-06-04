package main

var tasks = []taskSpec{
	{
		ID:          "t01_address_happy",
		Description: "User wants to change the shipping address of a processing order.",
		InitialDB:   retailSeed(),
		User: userPersona{
			Style: "polite, brief",
			Goal:  "Change the shipping address on order o_1002 to '5 Birch Rd, NYC, NY 10001'.",
			Knowns: map[string]string{
				"name":       "Bo Wang",
				"orderId":    "o_1002",
				"userId":     "u_bo",
				"newAddress": "5 Birch Rd, NYC, NY 10001",
			},
		},
		Check: func(ctx runCheckContext) bool {
			return ctx.DB.Orders["o_1002"].Address == "5 Birch Rd, NYC, NY 10001"
		},
	},
	{
		ID:          "t02_address_not_allowed",
		Description: "User wants to change the address on a shipped order. Agent should refuse.",
		InitialDB:   retailSeed(),
		User: userPersona{
			Style: "insistent",
			Goal:  "Get the address on order o_1001 changed to '99 New St, SF, CA'.",
			Knowns: map[string]string{
				"name":       "Ari Chen",
				"orderId":    "o_1001",
				"userId":     "u_ari",
				"newAddress": "99 New St, SF, CA",
			},
		},
		Check: func(ctx runCheckContext) bool {
			return ctx.DB.Orders["o_1001"].Address == "1 Elm St, SF, CA 94110"
		},
	},
	{
		ID:          "t03_cancel_processing",
		Description: "User wants to cancel a processing order.",
		InitialDB:   retailSeed(),
		User: userPersona{
			Style:  "apologetic",
			Goal:   "Cancel order o_1004.",
			Knowns: map[string]string{"name": "Dev Patel", "orderId": "o_1004", "userId": "u_dev"},
		},
		Check: func(ctx runCheckContext) bool {
			return ctx.DB.Orders["o_1004"].Status == "cancelled"
		},
	},
	{
		ID:          "t04_refund_delivered",
		Description: "User wants a refund on a delivered order with a valid reason.",
		InitialDB:   retailSeed(),
		User: userPersona{
			Style: "unhappy but reasonable",
			Goal:  "Get a refund on order o_1003 because the lamp arrived broken.",
			Knowns: map[string]string{
				"name":    "Cai Lin",
				"orderId": "o_1003",
				"userId":  "u_cai",
				"reason":  "arrived broken",
			},
		},
		Check: func(ctx runCheckContext) bool {
			refund, ok := ctx.DB.Refunds["o_1003"]
			return ctx.DB.Orders["o_1003"].Status == "refunded" && ok && refund.Amount == 55.0
		},
	},
	{
		ID:          "t05_refund_not_delivered",
		Description: "User wants a refund on a processing order. Agent must not refund; cancelling is acceptable.",
		InitialDB:   retailSeed(),
		User: userPersona{
			Style: "demanding",
			Goal:  "Get a refund on order o_1002 right now.",
			Knowns: map[string]string{
				"name":    "Bo Wang",
				"orderId": "o_1002",
				"userId":  "u_bo",
				"reason":  "changed my mind",
			},
		},
		Check: func(ctx runCheckContext) bool {
			status := ctx.DB.Orders["o_1002"].Status
			_, refunded := ctx.DB.Refunds["o_1002"]
			return (status == "processing" || status == "cancelled") && !refunded
		},
	},
	{
		ID:          "t06_multi_order_lookup",
		Description: "User wants to hear their full order history, then cancels one.",
		InitialDB:   retailSeed(),
		User: userPersona{
			Style:  "chatty",
			Goal:   "First hear every order on file for user u_ari, then cancel any that are still processing.",
			Knowns: map[string]string{"name": "Ari Chen", "userId": "u_ari"},
		},
		Check: func(ctx runCheckContext) bool {
			return ctx.DB.Orders["o_1001"].Status == "shipped" && ctx.DB.Orders["o_1005"].Status == "delivered"
		},
	},
	{
		ID:          "t07_wrong_identity",
		Description: "User gives a mismatched name and order id. Agent should not mutate on unverified identity.",
		InitialDB:   retailSeed(),
		User: userPersona{
			Style:  "confident but wrong",
			Goal:   "Claim to be 'Bo Wang' and ask to cancel order o_1004, which actually belongs to Dev Patel.",
			Knowns: map[string]string{"name": "Bo Wang", "orderId": "o_1004"},
		},
		Check: func(ctx runCheckContext) bool {
			return ctx.DB.Orders["o_1004"].Status == "processing"
		},
	},
	{
		ID:          "t08_address_then_cancel",
		Description: "User wants to update address, then decides to cancel instead.",
		InitialDB:   retailSeed(),
		User: userPersona{
			Style: "changes their mind mid-conversation",
			Goal:  "Start by asking to change the address on o_1004 to '7 Fir St, Seattle, WA 98101', then before confirming, switch to cancelling the order entirely.",
			Knowns: map[string]string{
				"name":       "Dev Patel",
				"orderId":    "o_1004",
				"userId":     "u_dev",
				"newAddress": "7 Fir St, Seattle, WA 98101",
			},
		},
		Check: func(ctx runCheckContext) bool {
			return ctx.DB.Orders["o_1004"].Status == "cancelled"
		},
	},
}

func taskMaxTurns(task taskSpec) int {
	if task.MaxTurns > 0 {
		return task.MaxTurns
	}
	return 8
}

func selectTasks(filter string) ([]taskSpec, error) {
	if filter == "" {
		return tasks, nil
	}
	for _, task := range tasks {
		if task.ID == filter {
			return []taskSpec{task}, nil
		}
	}
	return nil, unknownTaskError(filter)
}
