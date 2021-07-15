#!/usr/bin/env bats 
#
# Test for the scenario that
# "podman import" tarball exported from a container and modified the content.
# 

load helpers

alpine="quay.io/libpod/alpine"

@test "Implied pull, build, export, modify, import, tag, run, kill" {

  # Create a test file following test
  echo "NOT modified tar file" >> $PODMAN_TMPDIR/testfile1
  echo "modified tar file" >> $PODMAN_TMPDIR/testfile2
  

  # Create Dockerfile for test
  dockerfile=$PODMAN_TMPDIR/Dockerfile
  
  # FIXME: If ADD file to /tmp in Dockerfile is only testfile1, 
  #        ERROR occurs at 'tar -rf' or 'podman import'.
  #        It seems to cause "tar -tf" get error.
  #        It reproduce if comment out 'ADD prevent-error-tarball /tmp'
  #        In the condition, 'FROM $IMAGE' success but, 'FROM $alpine' failed
  touch $PODMAN_TMPDIR/prevent-error-tarball

  cat >$dockerfile <<EOF
FROM $alpine
ADD testfile1 /tmp
#ADD prevent-error-tarball /tmp
WORKDIR /tmp
EOF

  b_img=before_change_img
  b_cnt=before_change_cnt
  a_img=after_change_img
  a_cnt=after_change_cnt

  # Build from Dockerfile FROM non-existing local image
  run_podman build -t $b_img $PODMAN_TMPDIR
  run_podman run -d --name $b_cnt $b_img sleep 60

  # Export built container as tarball
  run_podman export -o $PODMAN_TMPDIR/$b_cnt.tar $b_cnt
  run_podman rm -fa

  # Modify tarball contents
  tar --delete -f $PODMAN_TMPDIR/$b_cnt.tar tmp/testfile1

  # FOR DEBUG: Look ERROR when only 'ADD testfile1 /tmp'
  echo
  echo "** FOR DEBUG **"
  echo "# tar -tf $PODMAN_TMPDIR/$b_cnt.tar | grep testfile"
  run grep testfile <(tar -tf $PODMAN_TMPDIR/$b_cnt.tar)
  echo "** ERROR MESSAGE **"
  echo "$output"
  echo

  run tar -rf $PODMAN_TMPDIR/$b_cnt.tar $PODMAN_TMPDIR/testfile2
  # FOR DEBUG: When add a file to tarball, output the messages.
  dprint ""
  dprint "** FOR DEBUG **"
  dprint "#  tar -rf $PODMAN_TMPDIR/$b_cnt.tar $PODMAN_TMPDIR/testfile2"
  for ((i=0; i<${#lines[@]}; i++)); do
    dprint "${lines[i]}"
  done
  dprint ""

  # Import tarball
  run_podman import -q $PODMAN_TMPDIR/$b_cnt.tar \
      --change "CMD sh -c \
      \"trap 'exit 33' 2;
      echo READY;
      cat $PODMAN_TMPDIR/testfile2;
      while true; do sleep 0.05;done\""
  iid=$output

  # Tag imported image
  run_podman tag $iid $a_img
  
  # Run imported image to confirm tarball modification, block on non-special signal
  run_podman run --name $a_cnt -d $a_img
 
  # Run 'logs -f' on that container.
  # It is redirected to a named pipe in the backgroud,
  # to prevent a race condition that this test
  # - read the log before that contaier writie.
  # - send a signal before that container execute "trap"
  local fifo=$PODMAN_TMPDIR/podman-scenario-fifo.$(random_string 10)
  mkfifo $fifo
  $PODMAN logs -f $a_cnt >$fifo </dev/null &

  # Open the FIFO for reading, and keep it open.
  # With this exec we keep the FD open,
  # allowing 'read -t' to time out and report a useful error.
  exec 5<$fifo

  # Confirm import --change option worked
  # read container log redirected to the fifo
  read -t 10 -u 5 ready
  is "$ready" "READY" "ready log from container"

  # Confirm tarball is modified
  read -t 10 -u 5 modify
  is "$modify" "modified tar file" "modify tarball content"
  
  # Kill can send non-TERM/KILL signal to container to exit
  run_podman kill --signal 2 $a_cnt 
  run_podman wait $a_cnt
 
  # Confirm exit within timeout
  run_podman ps -a --filter name=$a_cnt --format '{{.Status}}'
  is "$output" "Exited (33)" "Exit by non-TERM/KILL"
  
  run_podman rmi -fa

}
# vim: filetype=sh
