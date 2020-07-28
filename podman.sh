#!/bin/bash
sudo podman run \
    -it --rm --net=host \
    --volume $(pwd):/root/dev \
    --volume ~/.ssh:/root/.ssh \
    --volume ~/.bashrc:/root/.bashrc \
    --volume ~/.gitconfig:/root/.gitconfig \
    --name entrypoint --hostname entrypoint \
  docker.io/containercraft/ccio-golang:ubi8 -c /usr/bin/tmux
