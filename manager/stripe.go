package manager

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stripe/stripe-go/v86"
	"github.com/stripe/stripe-go/v86/webhook"
	"github.com/vertrai/hub/manager/schema"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type StripeConfig struct {
	Enabled                                                                  bool
	SecretKey, WebhookSecret, SuccessURL, CancelURL, PortalReturnURL         string
	ManagedPayments, RequireTermsOfServiceConsent, StopAgentOnPaymentFailure bool
}
type stripeGateway interface {
	Checkout(context.Context, *stripe.CheckoutSessionCreateParams) (*stripe.CheckoutSession, error)
	Portal(context.Context, string, string) (string, error)
	Subscription(context.Context, string) (*stripe.Subscription, error)
}
type stripeSDK struct{ client *stripe.Client }

func newStripeGateway(cfg StripeConfig) stripeGateway {
	return &stripeSDK{stripe.NewClient(cfg.SecretKey)}
}
func (s *stripeSDK) Checkout(ctx context.Context, p *stripe.CheckoutSessionCreateParams) (*stripe.CheckoutSession, error) {
	return s.client.V1CheckoutSessions.Create(ctx, p)
}
func (s *stripeSDK) Portal(ctx context.Context, customer, url string) (string, error) {
	r, e := s.client.V1BillingPortalSessions.Create(ctx, &stripe.BillingPortalSessionCreateParams{Customer: stripe.String(customer), ReturnURL: stripe.String(url)})
	if e != nil {
		return "", e
	}
	return r.URL, nil
}
func (s *stripeSDK) Subscription(ctx context.Context, id string) (*stripe.Subscription, error) {
	p := &stripe.SubscriptionRetrieveParams{}
	p.AddExpand("latest_invoice")
	return s.client.V1Subscriptions.Retrieve(ctx, id, p)
}
func (m *Manager) createCheckoutSession(c *gin.Context) {
	var req struct {
		Product         string `json:"product"`
		Quantity        int64  `json:"quantity"`
		ConsentAccepted bool   `json:"consentAccepted"`
	}
	if c.ShouldBindJSON(&req) != nil || req.Product == "" || (req.Quantity != 0 && req.Quantity != 1) {
		c.JSON(400, gin.H{"error": "product and one agent seat are required"})
		return
	}
	cfg, gateway, loadErr := m.stripeRuntime()
	if loadErr != nil {
		c.JSON(503, gin.H{"error": "Stripe settings unavailable"})
		return
	}
	if !cfg.Enabled || cfg.SecretKey == "" || cfg.WebhookSecret == "" || cfg.SuccessURL == "" || cfg.CancelURL == "" {
		c.JSON(503, gin.H{"error": "Stripe checkout is not configured"})
		return
	}
	entry, err := m.commerceProduct(req.Product)
	if err != nil || !webSubscriptionRequired(entry) || entry.StripePriceID == "" {
		c.JSON(503, gin.H{"error": "subscription product is unavailable"})
		return
	}
	if entry.Web != nil && webRequiresConsent(entry) && !req.ConsentAccepted {
		c.JSON(400, gin.H{"error": "consent required"})
		return
	}
	// Reserve and COMMIT the idempotency key before calling Stripe. A lost
	// network response or database save must not create a second subscription.
	var record schema.Billing
	err = m.wdb.Db.Transaction(func(tx *gorm.DB) error {
		var user schema.User
		if e := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&user, "id = ?", mustWebUser(c)).Error; e != nil {
			return e
		}
		e := tx.Where("user_id = ? AND product = ? AND status = ? AND created_at > ?", user.ID, req.Product, "checkout_pending", time.Now().Add(-23*time.Hour)).Order("created_at desc").First(&record).Error
		if e == nil {
			return nil
		}
		if !errors.Is(e, gorm.ErrRecordNotFound) {
			return e
		}
		if e := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&entry, "id = ? AND product_id = ?", entry.ID, req.Product).Error; e != nil {
			return e
		}
		if !webSubscriptionRequired(entry) || entry.Module == "" || entry.StripePriceID == "" || !commercePublished(entry) || (entry.Web != nil && webRequiresConsent(entry) && !req.ConsentAccepted) {
			return errors.New("subscription product is unavailable")
		}
		record = schema.Billing{ID: commerceID("bill_"), UserID: user.ID, Product: req.Product, CatalogID: entry.ID, Module: entry.Module, PriceID: entry.StripePriceID, Status: "checkout_pending"}
		return tx.Create(&record).Error
	})
	if err != nil {
		c.JSON(500, gin.H{"error": "cannot reserve checkout"})
		return
	}
	var sessionURL, sessionID string
	err = m.wdb.Db.Transaction(func(tx *gorm.DB) error {
		if e := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&record, "id = ?", record.ID).Error; e != nil {
			return e
		}
		var user schema.User
		if e := tx.First(&user, "id = ?", record.UserID).Error; e != nil {
			return e
		}
		// Recheck web publication even when reusing a pending checkout.
		var current schema.AgentCatalogEntry
		if e := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&current, "id = ?", record.CatalogID).Error; e != nil {
			return e
		}
		if !webSubscriptionRequired(current) || !commercePublished(current) || current.Module == "" || current.StripePriceID == "" || (current.Web != nil && webRequiresConsent(current) && !req.ConsentAccepted) {
			return errors.New("subscription product is unavailable")
		}
		var e error
		if record.CheckoutSessionID != nil && record.CheckoutURL != "" {
			sessionID = *record.CheckoutSessionID
			sessionURL = record.CheckoutURL
			return nil
		}
		metadata := map[string]string{"billing_id": record.ID, "user_id": user.ID, "product": record.Product}
		p := &stripe.CheckoutSessionCreateParams{Mode: stripe.String("subscription"), ClientReferenceID: stripe.String(user.ID), SuccessURL: stripe.String(cfg.SuccessURL), CancelURL: stripe.String(cfg.CancelURL), LineItems: []*stripe.CheckoutSessionCreateLineItemParams{{Price: stripe.String(record.PriceID), Quantity: stripe.Int64(1)}}, Metadata: metadata, SubscriptionData: &stripe.CheckoutSessionCreateSubscriptionDataParams{Metadata: metadata}}
		p.SetIdempotencyKey(record.ID)
		var previous schema.Billing
		e = tx.Where("user_id = ? AND customer_id <> ''", user.ID).Order("created_at desc").First(&previous).Error
		if e == nil {
			p.Customer = stripe.String(previous.CustomerID)
		} else if errors.Is(e, gorm.ErrRecordNotFound) {
			p.CustomerEmail = stripe.String(user.Email)
		} else {
			return e
		}
		if cfg.ManagedPayments {
			p.ManagedPayments = &stripe.CheckoutSessionCreateManagedPaymentsParams{Enabled: stripe.Bool(true)}
		}
		if cfg.RequireTermsOfServiceConsent {
			p.ConsentCollection = &stripe.CheckoutSessionCreateConsentCollectionParams{TermsOfService: stripe.String("required")}
		}
		ctx, cancel := context.WithTimeout(c.Request.Context(), 20*time.Second)
		defer cancel()
		session, e := gateway.Checkout(ctx, p)
		if e != nil {
			return e
		}
		sessionID, sessionURL = session.ID, session.URL
		record.CheckoutSessionID = &sessionID
		record.CheckoutURL = sessionURL
		if session.Customer != nil {
			record.CustomerID = session.Customer.ID
		}
		return tx.Save(&record).Error
	})
	if err != nil {
		log.Error("create checkout", "err", err)
		c.JSON(502, gin.H{"error": "could not create checkout; please retry"})
		return
	}
	c.JSON(200, gin.H{"sessionId": sessionID, "url": sessionURL})
}
func (m *Manager) listBilling(c *gin.Context) {
	rows := []schema.Billing{}
	if err := m.wdb.Db.Where("user_id = ?", mustWebUser(c)).Order("created_at desc").Find(&rows).Error; err != nil {
		c.JSON(500, gin.H{"error": "cannot list billing"})
		return
	}
	c.JSON(200, gin.H{"items": rows})
}
func (m *Manager) adminBilling(c *gin.Context) {
	if !m.catalogDB(c) {
		return
	}
	rows := []schema.Billing{}
	if err := m.wdb.Db.Order("created_at desc").Limit(1000).Find(&rows).Error; err != nil {
		c.JSON(500, gin.H{"error": "cannot list billing"})
		return
	}
	c.JSON(200, gin.H{"items": rows})
}
func (m *Manager) getCheckoutSessionStatus(c *gin.Context) {
	var b schema.Billing
	if m.wdb.Db.First(&b, "checkout_session_id = ? AND user_id = ?", c.Param("sessionId"), mustWebUser(c)).Error != nil {
		c.JSON(404, gin.H{"error": "checkout session not found"})
		return
	}
	c.JSON(200, gin.H{"billing": b})
}
func (m *Manager) createPortalSession(c *gin.Context) {
	cfg, gateway, loadErr := m.stripeRuntime()
	if loadErr != nil {
		c.JSON(503, gin.H{"error": "Stripe settings unavailable"})
		return
	}
	if !cfg.Enabled || cfg.SecretKey == "" || cfg.PortalReturnURL == "" {
		c.JSON(503, gin.H{"error": "billing portal is not configured"})
		return
	}
	var b schema.Billing
	if m.wdb.Db.Where("user_id = ? AND customer_id <> ''", mustWebUser(c)).Order("created_at desc").First(&b).Error != nil {
		c.JSON(404, gin.H{"error": "no billing account found"})
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), 20*time.Second)
	defer cancel()
	url, err := gateway.Portal(ctx, b.CustomerID, cfg.PortalReturnURL)
	if err != nil {
		c.JSON(502, gin.H{"error": "could not open billing portal"})
		return
	}
	c.JSON(200, gin.H{"url": url})
}
func (m *Manager) stripeWebhook(c *gin.Context) {
	cfg, gateway, loadErr := m.stripeRuntime()
	if loadErr != nil || cfg.WebhookSecret == "" || m.wdb == nil {
		c.JSON(503, gin.H{"error": "Stripe webhook is unavailable"})
		return
	}
	raw, err := io.ReadAll(http.MaxBytesReader(c.Writer, c.Request.Body, 1<<20))
	if err != nil {
		c.JSON(400, gin.H{"error": "invalid webhook body"})
		return
	}
	event, err := webhook.ConstructEvent(raw, c.GetHeader("Stripe-Signature"), cfg.WebhookSecret)
	if err != nil {
		c.JSON(400, gin.H{"error": "invalid webhook signature or API version"})
		return
	}
	if err = m.processStripeEventWithRuntime(c.Request.Context(), event, cfg, gateway); err != nil {
		log.Error("process Stripe event", "event", event.ID, "err", err)
		c.JSON(500, gin.H{"error": "webhook processing failed; retry required"})
		return
	}
	c.JSON(200, gin.H{"received": true})
}

// An event and its entitlement/job updates commit together. Deployment never
// runs in the webhook. Retrieve current subscription state to tolerate delivery
// out of order (including a past invoice.paid arriving after cancellation).
func (m *Manager) processStripeEvent(ctx context.Context, event stripe.Event) error {
	cfg, gateway, err := m.stripeRuntime()
	if err != nil {
		return err
	}
	return m.processStripeEventWithRuntime(ctx, event, cfg, gateway)
}

func (m *Manager) processStripeEventWithRuntime(ctx context.Context, event stripe.Event, cfg StripeConfig, gateway stripeGateway) error {
	return m.wdb.Db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		result := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&schema.StripeEvent{ID: event.ID, Type: string(event.Type)})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected == 0 {
			return nil
		}
		switch string(event.Type) {
		case "checkout.session.completed", "checkout.session.expired":
			var s stripe.CheckoutSession
			if err := json.Unmarshal(event.Data.Raw, &s); err != nil {
				return err
			}
			var b schema.Billing
			err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ? OR checkout_session_id = ?", s.Metadata["billing_id"], s.ID).First(&b).Error
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return nil
			}
			if err != nil {
				return err
			}
			if s.ClientReferenceID != "" && s.ClientReferenceID != b.UserID {
				return errors.New("checkout owner mismatch")
			}
			if b.CheckoutSessionID != nil && *b.CheckoutSessionID != s.ID {
				return errors.New("checkout mismatch")
			}
			b.CheckoutSessionID = &s.ID
			if s.Customer != nil {
				b.CustomerID = s.Customer.ID
			}
			if s.Subscription != nil {
				if b.SubscriptionID != nil && *b.SubscriptionID != s.Subscription.ID {
					return errors.New("subscription mismatch")
				}
				b.SubscriptionID = &s.Subscription.ID
			}
			if b.Status == "checkout_pending" {
				b.Status = "checkout_completed"
				if string(event.Type) == "checkout.session.expired" {
					b.Status = "checkout_expired"
				}
			}
			return tx.Save(&b).Error
		case "invoice.paid", "invoice.payment_failed", "customer.subscription.updated", "customer.subscription.deleted":
			var subscriptionID string
			var metadata map[string]string
			if string(event.Type) == "invoice.paid" || string(event.Type) == "invoice.payment_failed" {
				var invoice stripe.Invoice
				if err := json.Unmarshal(event.Data.Raw, &invoice); err != nil {
					return err
				}
				if invoice.Parent != nil && invoice.Parent.SubscriptionDetails != nil {
					d := invoice.Parent.SubscriptionDetails
					if d.Subscription != nil {
						subscriptionID = d.Subscription.ID
					}
					metadata = d.Metadata
				}
			} else {
				var s stripe.Subscription
				if err := json.Unmarshal(event.Data.Raw, &s); err != nil {
					return err
				}
				subscriptionID = s.ID
				metadata = s.Metadata
			}
			if subscriptionID == "" {
				return nil
			}
			var b schema.Billing
			err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("subscription_id = ? OR id = ?", subscriptionID, metadata["billing_id"]).First(&b).Error
			// Events from other products sharing a Stripe account are not Hub orders.
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return nil
			}
			if err != nil {
				return err
			}
			if b.SubscriptionID != nil && *b.SubscriptionID != subscriptionID {
				return errors.New("subscription mismatch")
			}
			fetchCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
			defer cancel()
			s, err := gateway.Subscription(fetchCtx, subscriptionID)
			if err != nil {
				return err
			}
			if s.ID != subscriptionID || s.Metadata["billing_id"] != b.ID || s.Metadata["user_id"] != b.UserID || s.Metadata["product"] != b.Product {
				return errors.New("subscription metadata mismatch")
			}
			if s.Items == nil || len(s.Items.Data) != 1 || s.Items.Data[0].Price == nil || s.Items.Data[0].Price.ID != b.PriceID || s.Items.Data[0].Quantity != 1 {
				return errors.New("subscription price or quantity mismatch")
			}
			b.SubscriptionID = &subscriptionID
			b.Status = string(s.Status)
			b.CancelAtPeriodEnd = s.CancelAtPeriodEnd
			if s.Customer != nil {
				b.CustomerID = s.Customer.ID
			}
			item := s.Items.Data[0]
			b.CurrentPeriodStart = time.Unix(item.CurrentPeriodStart, 0).UTC()
			b.CurrentPeriodEnd = time.Unix(item.CurrentPeriodEnd, 0).UTC()
			if s.LatestInvoice != nil {
				b.InvoiceURL = s.LatestInvoice.HostedInvoiceURL
				b.InvoicePDF = s.LatestInvoice.InvoicePDF
			}
			entitled := (s.Status == stripe.SubscriptionStatusActive || s.Status == stripe.SubscriptionStatusTrialing)
			// A paid invoice (or actual trial) is necessary before the initial grant.
			paid := s.Status == stripe.SubscriptionStatusTrialing || (s.LatestInvoice != nil && s.LatestInvoice.Status == stripe.InvoiceStatusPaid)
			if entitled && paid && b.AgentID == "" {
				a := schema.WebAgent{ID: commerceID("agent_"), UserID: b.UserID, Product: b.Product, CatalogID: b.CatalogID, Module: b.Module, Source: "stripe:" + subscriptionID, State: "queued", Desired: "running"}
				if err := tx.Create(&a).Error; err != nil {
					return err
				}
				b.AgentID = a.ID
			}
			if b.AgentID != "" {
				desired := ""
				if entitled && paid {
					desired = "running"
				} else if s.Status == stripe.SubscriptionStatusCanceled || s.Status == stripe.SubscriptionStatusUnpaid || s.Status == stripe.SubscriptionStatusIncompleteExpired || s.Status == stripe.SubscriptionStatusPaused || (s.Status == stripe.SubscriptionStatusPastDue && cfg.StopAgentOnPaymentFailure) {
					desired = "stopped"
				}
				if desired != "" {
					if err := tx.Model(&schema.WebAgent{}).Where("id = ?", b.AgentID).Update("desired", desired).Error; err != nil {
						return err
					}
				}
			}
			b.LastEventCreated = event.Created
			return tx.Save(&b).Error
		}
		return nil
	})
}
