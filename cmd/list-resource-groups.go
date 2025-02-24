// Copyright (C) 2022 Specter Ops, Inc.
//
// This file is part of AzureHound.
//
// AzureHound is free software: you can redistribute it and/or modify
// it under the terms of the GNU General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
//
// AzureHound is distributed in the hope that it will be useful,
// but WITHOUT ANY WARRANTY; without even the implied warranty of
// MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE.  See the
// GNU General Public License for more details.
//
// You should have received a copy of the GNU General Public License
// along with this program.  If not, see <https://www.gnu.org/licenses/>.

package cmd

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"sync"
	"time"

	"github.com/bloodhoundad/azurehound/client"
	"github.com/bloodhoundad/azurehound/enums"
	"github.com/bloodhoundad/azurehound/models"
	"github.com/bloodhoundad/azurehound/pipeline"
	"github.com/spf13/cobra"
)

func init() {
	listRootCmd.AddCommand(listResourceGroupsCmd)
}

var listResourceGroupsCmd = &cobra.Command{
	Use:          "resource-groups",
	Long:         "Lists Azure Resource Groups",
	Run:          listResourceGroupsCmdImpl,
	SilenceUsage: true,
}

func listResourceGroupsCmdImpl(cmd *cobra.Command, args []string) {
	ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt, os.Kill)
	defer gracefulShutdown(stop)

	log.V(1).Info("testing connections")
	if err := testConnections(); err != nil {
		exit(err)
	} else if azClient, err := newAzureClient(); err != nil {
		exit(err)
	} else {
		log.Info("collecting azure resource groups...")
		start := time.Now()
		stream := listResourceGroups(ctx, azClient, listSubscriptions(ctx, azClient))
		outputStream(ctx, stream)
		duration := time.Since(start)
		log.Info("collection completed", "duration", duration.String())
	}
}

func listResourceGroups(ctx context.Context, client client.AzureClient, subscriptions <-chan interface{}) <-chan interface{} {
	var (
		out     = make(chan interface{})
		ids     = make(chan string)
		streams = pipeline.Demux(ctx.Done(), ids, 25)
		wg      sync.WaitGroup
	)

	go func() {
		defer close(ids)

		for result := range pipeline.OrDone(ctx.Done(), subscriptions) {
			if subscription, ok := result.(AzureWrapper).Data.(models.Subscription); !ok {
				log.Error(fmt.Errorf("failed type assertion"), "unable to continue enumerating resource groups", "result", result)
				return
			} else {
				ids <- subscription.SubscriptionId
			}
		}
	}()

	wg.Add(len(streams))
	for i := range streams {
		stream := streams[i]
		go func() {
			defer wg.Done()
			for id := range stream {
				count := 0
				for item := range client.ListAzureResourceGroups(ctx, id.(string), "") {
					if item.Error != nil {
						log.Error(item.Error, "unable to continue processing resource groups for this subscription", "subscriptionId", id)
					} else {
						resourceGroup := models.ResourceGroup{
							ResourceGroup:  item.Ok,
							SubscriptionId: item.SubscriptionId,
						}
						log.V(2).Info("found resource group", "resourceGroup", resourceGroup)
						count++
						out <- AzureWrapper{
							Kind: enums.KindAZResourceGroup,
							Data: resourceGroup,
						}
					}
				}
				log.V(1).Info("finished listing resource groups", "subscriptionId", id, "count", count)
			}
		}()
	}

	go func() {
		wg.Wait()
		close(out)
		log.Info("finished listing all resource groups")
	}()

	return out
}
