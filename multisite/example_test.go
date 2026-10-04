// SPDX-License-Identifier: MPL-2.0
package multisite_test

import (
	"context"
	"fmt"
	"time"

	"github.com/AChWorks/achrix"
	"github.com/AChWorks/achrix/multisite"
)

func ExampleService_Resolve() {
	resolver, err := multisite.New(multisite.Config{Sites: []multisite.Site{
		{ID: "site-a", Authorities: []string{"a.example", "a.example:443"}},
		{ID: "site-b", Authorities: []string{"b.example"}},
	}})
	if err != nil {
		panic(err)
	}
	// This example's product policy grants only authority resolution. Actual
	// authentication, trusted ingress and site resource authorization are separate.
	policy := achrix.PolicyFunc(func(_ context.Context, actor achrix.Principal, capability, authority string) error {
		if actor == "product-router" && capability == multisite.Resolve && authority == "a.example" {
			return nil
		}
		return achrix.ErrDenied
	})
	app, err := achrix.New(achrix.Config{StartupTimeout: time.Second, ShutdownTimeout: time.Second}, policy, resolver)
	if err != nil {
		panic(err)
	}
	service, err := multisite.NewService(app, resolver)
	if err != nil {
		panic(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err = app.Start(ctx); err != nil {
		panic(err)
	}
	id, err := service.Resolve(ctx, "product-router", "a.example")
	if err != nil {
		panic(err)
	}
	fmt.Println(id)
	cleanup, cleanupCancel := context.WithTimeout(context.Background(), time.Second)
	defer cleanupCancel()
	if err = app.Shutdown(cleanup); err != nil {
		panic(err)
	}
	// Output: site-a
}
