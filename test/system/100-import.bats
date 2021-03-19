#!/usr/bin/env bats 
#
# Test for "podman import"
#

load helpers

ALPINE="quay.io/libpod/alpine"

function setup() {

  basic_setup

  # Create a test file following test
  echo "Import TEST" >> $PODMAN_TMPDIR/testfile

  # Create Dockerfile for test
  DOCKERFILE=$PODMAN_TMPDIR/Dockerfile

  cat >$DOCKERFILE <<EOF
FROM $ALPINE
ADD testfile /tmp
WORKDIR /tmp
CMD cat testfile
EOF
}

function teardown() {

  basic_teardown
  rm -rf $PODMAN_TMPDIR

}

@test "podman import changed tarball" {

  B_IMG=before_change_img
  B_CNT=before_change_cnt
  A_IMG=after_change_img
  A_CNT=after_change_cnt

  # Build from Dockerfile FROM non-existing local image
  run_podman build -t $B_IMG $PODMAN_TMPDIR
  run_podman run -d --name $B_CNT $B_IMG sleep 300

  # Export built container as tarball
  run_podman export -o $PODMAN_TMPDIR/$B_CNT.tar $B_CNT
  run_podman rm -fa

  DATE=$(date -u "+%a %b %d %R")
  # Modify tarball contents
  # Import tarball

  cmd="date;/bin/sh -c \"trap 'exit 33' 2;while true;do sleep 1;done\""
  run_podman import -q \
      --change "CMD $cmd" \
       $PODMAN_TMPDIR/$B_CNT.tar
  IID=$output

  # Tag imported image
  run_podman tag $IID $A_IMG
  
  # Run imported image to confirm tarball modification, block on non-special signal
  touch $PODMAN_TMPDIR/testlog
  run_podman run --name $A_CNT -d --log-driver=k8s-file \
             --log-opt="path=$PODMAN_TMPDIR/testlog" $A_IMG 
  
  run cat $PODMAN_TMPDIR/testlog
  is "$output" ".*${DATE}" "Confirm change image by checking CMD output"
  
  # Kill can send non-TERM/KILL signal to container to exit
  run_podman kill --signal 2 $A_CNT 
  sleep 10
 
  # Confirm exit within timeout
  run_podman ps -a --filter name=$A_CNT --format '{{.Status}}'
  is "$output" "Exited (33)" "Exit by non-TERM/KILL"
  
  run_podman rmi -fa

}
# vim: filetype=sh
