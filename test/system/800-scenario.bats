#!/usr/bin/env bats 
#
# Test for "podman import"
#

load helpers

alpine="quay.io/libpod/alpine"

@test "Implied pull, build, export, modify, import, tag, run, kill" {

  # Create a test file following test
  echo "Import TEST" >> $PODMAN_TMPDIR/testfile

  # Create Dockerfile for test
  dockerfile=$PODMAN_TMPDIR/Dockerfile

  cat >$dockerfile <<EOF
FROM $alpine
ADD testfile /tmp
WORKDIR /tmp
CMD cat testfile
EOF

  b_img=before_change_img
  b_cnt=before_change_cnt
  a_img=after_change_img
  a_cnt=after_change_cnt

  # Build from Dockerfile FROM non-existing local image
  run_podman build -t $b_img $PODMAN_TMPDIR
  run_podman run -d --name $b_cnt $b_img sleep 300

  # Export built container as tarball
  run_podman export -o $PODMAN_TMPDIR/$b_cnt.tar $b_cnt
  run_podman rm -fa

  date=$(date -u "+%a %b %d %R")
  # Modify tarball contents
  # Import tarball

  cmd="date;/bin/sh -c \"trap 'exit 33' 2;while true;do sleep 1;done\""
  run_podman import -q \
      --change "CMD $cmd" \
       $PODMAN_TMPDIR/$b_cnt.tar
  iid=$output

  # Tag imported image
  run_podman tag $iid $a_img
  
  # Run imported image to confirm tarball modification, block on non-special signal
  touch $PODMAN_TMPDIR/testlog
  run_podman run --name $a_cnt -d --log-driver=k8s-file \
             --log-opt="path=$PODMAN_TMPDIR/testlog" $a_img 
  
  run cat $PODMAN_TMPDIR/testlog
  is "$output" ".*${date}" "Confirm change image by checking CMD output"
  
  # Kill can send non-TERM/KILL signal to container to exit
  run_podman kill --signal 2 $a_cnt 
  sleep 10
 
  # Confirm exit within timeout
  run_podman ps -a --filter name=$a_cnt --format '{{.Status}}'
  is "$output" "Exited (33)" "Exit by non-TERM/KILL"
  
  run_podman rmi -fa

}
# vim: filetype=sh
