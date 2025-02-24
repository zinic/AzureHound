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
	listRootCmd.AddCommand(listServicePrincipalOwnersCmd)
}

var listServicePrincipalOwnersCmd = &cobra.Command{
	Use:          "service-principal-owners",
	Long:         "Lists Azure AD Service Principal Owners",
	Run:          listServicePrincipalOwnersCmdImpl,
	SilenceUsage: true,
}

func listServicePrincipalOwnersCmdImpl(cmd *cobra.Command, args []string) {
	ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt, os.Kill)
	defer gracefulShutdown(stop)

	log.V(1).Info("testing connections")
	if err := testConnections(); err != nil {
		exit(err)
	} else if azClient, err := newAzureClient(); err != nil {
		exit(err)
	} else {
		log.Info("collecting azure service principal owners...")
		start := time.Now()
		stream := listServicePrincipalOwners(ctx, azClient, listServicePrincipals(ctx, azClient))
		outputStream(ctx, stream)
		duration := time.Since(start)
		log.Info("collection completed", "duration", duration.String())
	}
}

func listServicePrincipalOwners(ctx context.Context, client client.AzureClient, servicePrincipals <-chan interface{}) <-chan interface{} {
	var (
		out     = make(chan interface{})
		ids     = make(chan string)
		streams = pipeline.Demux(ctx.Done(), ids, 25)
		wg      sync.WaitGroup
	)

	go func() {
		defer close(ids)

		for result := range pipeline.OrDone(ctx.Done(), servicePrincipals) {
			if servicePrincipal, ok := result.(AzureWrapper).Data.(models.ServicePrincipal); !ok {
				log.Error(fmt.Errorf("failed type assertion"), "unable to continue enumerating service principal owners", "result", result)
				return
			} else {
				ids <- servicePrincipal.Id
			}
		}
	}()

	wg.Add(len(streams))
	for i := range streams {
		stream := streams[i]
		go func() {
			defer wg.Done()
			for id := range stream {
				var (
					servicePrincipalOwners = models.ServicePrincipalOwners{
						ServicePrincipalId: id.(string),
					}
					count = 0
				)
				for item := range client.ListAzureADServicePrincipalOwners(ctx, id.(string), "", "", "", nil) {
					if item.Error != nil {
						log.Error(item.Error, "unable to continue processing owners for this service principal", "servicePrincipalId", id)
					} else {
						servicePrincipalOwner := models.ServicePrincipalOwner{
							Owner:              item.Ok,
							ServicePrincipalId: item.ServicePrincipalId,
						}
						log.V(2).Info("found service principal owner", "servicePrincipalOwner", servicePrincipalOwner)
						count++
						servicePrincipalOwners.Owners = append(servicePrincipalOwners.Owners, servicePrincipalOwner)
					}
				}
				out <- AzureWrapper{
					Kind: enums.KindAZServicePrincipalOwner,
					Data: servicePrincipalOwners,
				}
				log.V(1).Info("finished listing service principal owners", "servicePrincipalId", id, "count", count)
			}
		}()
	}

	go func() {
		wg.Wait()
		close(out)
		log.Info("finished listing all service principal owners")
	}()

	return out
}
