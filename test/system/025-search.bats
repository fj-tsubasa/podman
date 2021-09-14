#!/usr/bin/env bats   -*- bats -*-
#
# tests for podman search
#

load helpers

###############################################################################
# BEGIN one-time envariable setup

# Create a scratch directory; our podman registry will run from here. We
# also use it for other temporary files like authfiles.
if [ -z "${PODMAN_SEARCH_WORKDIR}" ]; then
    export PODMAN_SEARCH_WORKDIR=$(mktemp -d --tmpdir=${BATS_TMPDIR:-${TMPDIR:-/tmp}} podman_bats_search.XXXXXX)
fi

# Randomly-generated username and password
if [ -z "${PODMAN_SEARCH_USER}" ]; then
    export PODMAN_SEARCH_USER="user$(random_string 4)"
    export PODMAN_SEARCH_PASS=$(random_string 15)
fi

# Randomly-assigned port in the 5xxx range
if [ -z "${PODMAN_SEARCH_REGISTRY_PORT}" ]; then
    export PODMAN_SEARCH_REGISTRY_PORT=$(random_free_port)
fi

# Set a directory path for authentication certificates
AUTHDIR=${PODMAN_SEARCH_WORKDIR}/auth

# Override any user-set path to an auth file
unset REGISTRY_AUTH_FILE

# END   one-time envariable setup
###############################################################################
# BEGIN first "test" - start a registry for use by other tests
#
# This isn't really a test: it's a helper that starts a local registry.
# Note that we're careful to use a root/runroot separate from our tests,
# so setup/teardown don't clobber our registry image.
#

@test "podman search [start registry]" {
    mkdir -p $AUTHDIR

    # Registry image; copy of docker.io, but on our own registry
    local REGISTRY_IMAGE="$PODMAN_TEST_IMAGE_REGISTRY/$PODMAN_TEST_IMAGE_USER/registry:2.7"

    # Pull registry image, but into a separate container storage
    mkdir -p ${PODMAN_SEARCH_WORKDIR}/root
    mkdir -p ${PODMAN_SEARCH_WORKDIR}/runroot
    PODMAN_SEARCH_ARGS="--root ${PODMAN_SEARCH_WORKDIR}/root --runroot ${PODMAN_SEARCH_WORKDIR}/runroot"
    # Give it three tries, to compensate for flakes
    run_podman ${PODMAN_SEARCH_ARGS} pull $REGISTRY_IMAGE ||
        run_podman ${PODMAN_SEARCH_ARGS} pull $REGISTRY_IMAGE ||
        run_podman ${PODMAN_SEARCH_ARGS} pull $REGISTRY_IMAGE

    # Registry image needs a cert. Self-signed is good enough.
    CERT=$AUTHDIR/domain.crt
    if [ ! -e $CERT ]; then
        openssl req -newkey rsa:4096 -nodes -sha256 \
                -keyout $AUTHDIR/domain.key -x509 -days 2 \
                -out $CERT \
                -subj "/C=US/ST=Foo/L=Bar/O=Red Hat, Inc./CN=localhost" \
                -addext "subjectAltName = DNS:localhost"
   fi

    # Prepare credentials for TLS certification
    cp $CERT $AUTHDIR/domain.cert

    # Store credentials where container will see them
    if [ ! -e $AUTHDIR/htpasswd ]; then
        htpasswd -Bbn ${PODMAN_SEARCH_USER} ${PODMAN_SEARCH_PASS} \
                 > $AUTHDIR/htpasswd

        # In case $PODMAN_TEST_KEEP_SEARCH_REGISTRY is set, for testing later
        echo "${PODMAN_SEARCH_USER}:${PODMAN_SEARCH_PASS}" \
             > $AUTHDIR/htpasswd-plaintext
    fi

    # Run the registry container.
    run_podman '?' ${PODMAN_SEARCH_ARGS} rm -f registry
    run_podman ${PODMAN_SEARCH_ARGS} run -d \
               -p ${PODMAN_SEARCH_REGISTRY_PORT}:5000 \
               --name registry \
               -v $AUTHDIR:/auth:Z \
               -e "REGISTRY_AUTH=htpasswd" \
               -e "REGISTRY_AUTH_HTPASSWD_REALM=Registry Realm" \
               -e REGISTRY_AUTH_HTPASSWD_PATH=/auth/htpasswd \
               -e REGISTRY_HTTP_TLS_CERTIFICATE=/auth/domain.crt \
               -e REGISTRY_HTTP_TLS_KEY=/auth/domain.key \
               $REGISTRY_IMAGE

}

# END   first "test" - start a registry for use by other tests
###############################################################################
# BEGIN actual tests

@test "podman search - filter" {

    run_podman search --filter=is-official docker.io/registry

    official_column=(`echo "$output" | awk -F"  +" '{print $5}'`)

    # Confirm to get OFFICIAL column
    if expr "${official_column[0]}" : "OFFICIAL" >/dev/null; then
        official_column=("${official_column[@]:1}")
    else
        echo "expected field is not OFFICIAL"
        exit 1
    fi

    # TEST: get official image only
    local i
    for i in "${official_column[@]}"; do
        if [[ "${i}" != "[OK]" ]]; then
            echo "OFFICIAL field is not OK"
            exit 1
        fi
    done
}

@test "podman search - format and limit" {

    limit_num=3

    run_podman search --format "table {{.Index}} {{.Name}}" \
                      --limit $limit_num docker.io/registry

    index_column=(`echo "$output" | awk '{print $1}'`)
    name_column=(`echo "$output" | awk '{print $2}'`)

    # TEST: get column specified by --format option
    if expr "${index_column[0]}" : "INDEX" && expr "${name_column[0]}" : "NAME"; then
        index_column=("${index_column[@]:1}")
        name_column=("${name_column[@]:1}")
    else
        echo "--foramt option doesn't work"
        exit 1
    fi

    # TEST: get the number of image specified by --limit option
    if "${#index_column[@]}" != $limit_num; then
        echo "--limit option doesn't work"
        exit 1
    fi
}

@test "podman search - tls-verify and authfile" {

    destname=ok-$(random_string 10 | tr A-Z a-z)-ok

    run_podman login --tls-verify=true --cert-dir ${AUTHDIR} \
               --username ${PODMAN_SEARCH_USER} \
               --password-stdin \
               --authfile ${PODMAN_TMPDIR}/auth.json \
               localhost:${PODMAN_SEARCH_REGISTRY_PORT} <<< "${PODMAN_SEARCH_PASS}"

    run_podman push --tls-verify=true --cert-dir ${AUTHDIR} \
               --authfile ${PODMAN_TMPDIR}/auth.json \
               $IMAGE localhost:${PODMAN_SEARCH_REGISTRY_PORT}/$destname

    # TEST: search with TLS certification: expected ERROR
    run_podman 125 search --tls-verify=true \
               --authfile ${PODMAN_TMPDIR}/auth.json \
               --format "table {{.Name}}" \
               localhost:${PODMAN_SEARCH_REGISTRY_PORT}/$destname
    err=".* x509: certificate signed by unknown authority"
    is "${lines[1]}" "$err" "Confirm trying TLS verification"

    # TEST: search without TLS certification: expected OK
    run_podman search --tls-verify=false \
               --authfile ${PODMAN_TMPDIR}/auth.json \
               --format "table {{.Name}}" \
               localhost:${PODMAN_SEARCH_REGISTRY_PORT}/$destname
    is "${lines[1]}" "localhost:${PODMAN_SEARCH_REGISTRY_PORT}/$destname" "search output is destname"

    run_podman logout --authfile ${PODMAN_TMPDIR}/auth.json localhost:${PODMAN_SEARCH_REGISTRY_PORT}
}

# END actual tests
###############################################################################
# BEGIN teardown (remove the registry container)

@test "podman search [stop registry, clean up]" {

    # For manual debugging; user may request keeping the registry running
    if [ -n "${PODMAN_TEST_KEEP_SEARCH_REGISTRY}" ]; then
        skip "[leaving registry running by request]"
    fi

    run_podman --root    ${PODMAN_SEARCH_WORKDIR}/root   \
               --runroot ${PODMAN_SEARCH_WORKDIR}/runroot \
               rm -f registry
    run_podman --root    ${PODMAN_SEARCH_WORKDIR}/root   \
               --runroot ${PODMAN_SEARCH_WORKDIR}/runroot \
               rmi -a

    # By default, clean up
    if [ -z "${PODMAN_TEST_KEEP_SEARCH_WORKDIR}" ]; then
        rm -rf ${PODMAN_SEARCH_WORKDIR}
    fi

    # Make sure socket is closed
    if { exec 3<> /dev/tcp/127.0.0.1/${PODMAN_SEARCH_REGISTRY_PORT}; } &>/dev/null; then
        die "Socket still seems open"
    fi
}

# END   teardown (remove the registry container)
###############################################################################

# vim: filetype=sh
