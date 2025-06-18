#!/usr/bin/env bash

setup_dynamodb() {
    if [[ $# -ne 1 ]]; then
        echo "Usage: setup_dynamodb <name>"
        return 1
    fi

    local name="$1"
    local directory="/var/lib/machines/$name"

    sudo tee "$directory/etc/systemd/system/dynamodb.service" > /dev/null <<EOF
[Unit]
Description=DynamoDB Local
Documentation=https://docs.aws.amazon.com/amazondynamodb/latest/developerguide/DynamoDBLocal.html

After=network.target
Wants=network.target

[Service]
User=dynamodb
Type=simple
Environment=DDB_LOCAL_TELEMETRY=0
WorkingDirectory=/var/lib/dynamodb
ExecStart=/usr/bin/java -Djava.library.path=/opt/dynamodb-local/DynamoDBLocal_lib -jar /opt/dynamodb-local/DynamoDBLocal.jar -sharedDb -port 8000
Restart=always
RestartSec=1s

[Install]
WantedBy=multi-user.target
EOF

    # Create dynamodb user
    sudo mkdir -p "$directory/etc/sysusers.d"
    sudo tee "$directory/etc/sysusers.d/20-dynamodb.conf" > /dev/null <<EOF
# DynamoDB Local - https://docs.aws.amazon.com/amazondynamodb/latest/developerguide/DynamoDBLocal.html

#Type  Name      ID  GECOS               Home
u      dynamodb  -   "DynamoDB user"     /var/lib/dynamodb
EOF

    # Create tmpfiles configuration
    sudo mkdir -p "$directory/etc/tmpfiles.d"
    sudo tee "$directory/etc/tmpfiles.d/20-dynamodb.conf" > /dev/null <<EOF
#Type Path                    Mode User     Group    Age Argument…
d     /var/lib/dynamodb       0755 dynamodb dynamodb -   -
EOF

    # Bootstrap script
    sudo tee "$directory/bootstrap.sh" > /dev/null <<EOF
systemctl enable dynamodb.service
EOF

    sudo systemd-nspawn -D "$directory" bash /bootstrap.sh
}
