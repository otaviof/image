// Copyright 2020 The Shipwright Authors.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//       http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package main

import (
	"context"
	"crypto/tls"
	"fmt"
	"os"

	"k8s.io/client-go/tools/clientcmd"

	imgcopy "github.com/containers/image/v5/copy"
	"github.com/containers/image/v5/signature"
	"github.com/containers/image/v5/transports/alltransports"
	"github.com/containers/image/v5/types"
	"github.com/spf13/cobra"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"

	"github.com/shipwright-io/image/cmd/kubectl-image/static"
	"github.com/shipwright-io/image/infra/pb"
	"github.com/shipwright-io/image/infra/progbar"
)

func init() {
	imagepull.Flags().Bool("insecure", false, "don't verify certificate when connecting")
}

var imagepull = &cobra.Command{
	Use:     "pull <imgctrl.instance:port/namespace/name>",
	Short:   "Pulls current image version",
	Long:    static.Text["pull_help_header"],
	Example: static.Text["pull_help_examples"],
	RunE: func(c *cobra.Command, args []string) error {
		if len(args) != 1 {
			return fmt.Errorf("invalid number of arguments")
		}

		insecure, err := c.Flags().GetBool("insecure")
		if err != nil {
			return err
		}

		config, err := clientcmd.BuildConfigFromFlags("", os.Getenv("KUBECONFIG"))
		if err != nil {
			return err
		}

		if config.BearerToken == "" {
			return fmt.Errorf("empty token, you need a kubernetes token to pull")
		}

		// first understands what tag is the user referring to.
		tidx, err := indexFor(args[0])
		if err != nil {
			return err
		}

		// now that we know what is the tag we do the grpc call
		// to retrieve the image. The output here is a local tar
		// file from where we can load the image into runtime's
		// local storage.
		srcref, cleanup, err := pullImage(c.Context(), tidx, config.BearerToken, insecure)
		if err != nil {
			return err
		}
		defer cleanup()

		dstref, err := tidx.localStorageRef()
		if err != nil {
			return err
		}

		pol := &signature.Policy{
			Default: signature.PolicyRequirements{
				signature.NewPRInsecureAcceptAnything(),
			},
		}
		polctx, err := signature.NewPolicyContext(pol)
		if err != nil {
			return err
		}

		// copy the image into runtime's local storage.
		_, err = imgcopy.Image(
			c.Context(), polctx, dstref, srcref, &imgcopy.Options{},
		)
		return err
	},
}

// pullImage pulls the current generation for an image identified by imageindex.
// Returns a reference to the locally stored image (on disk) and a function to
// be called at the end to clean up our mess. If this function returns an error
// then callers don't need to call the clean-up function.
func pullImage(
	ctx context.Context, idx imageindex, token string, insecure bool,
) (types.ImageReference, func(), error) {
	conn, err := grpc.DialContext(
		ctx,
		idx.server,
		grpc.WithTransportCredentials(
			credentials.NewTLS(&tls.Config{
				InsecureSkipVerify: insecure,
			}),
		),
	)
	if err != nil {
		return nil, nil, fmt.Errorf("error connecting: %w", err)
	}

	header := &pb.Header{
		Name:      idx.name,
		Namespace: idx.namespace,
		Token:     token,
	}

	client := pb.NewImageIOServiceClient(conn)
	stream, err := client.Pull(
		ctx,
		&pb.Packet{
			TestOneof: &pb.Packet_Header{
				Header: header,
			},
		},
	)
	if err != nil {
		return nil, nil, fmt.Errorf("error pulling: %w", err)
	}

	fsh, err := createHomeTempDir()
	if err != nil {
		return nil, nil, fmt.Errorf("error creating temp dir: %w", err)
	}

	fp, cleanup, err := fsh.TempFile()
	if err != nil {
		return nil, nil, fmt.Errorf("error creating temp file: %w", err)
	}

	pbar := progbar.New(ctx, "Pulling")
	defer pbar.Wait()

	if err := pb.Receive(stream, fp, pbar); err != nil {
		cleanup()
		return nil, nil, fmt.Errorf("error receiving file: %w", err)
	}

	str := fmt.Sprintf("docker-archive:%s", fp.Name())
	fromref, err := alltransports.ParseImageName(str)
	if err != nil {
		cleanup()
		return nil, nil, fmt.Errorf("error parsing reference: %w", err)
	}

	return fromref, cleanup, nil
}
