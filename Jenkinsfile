#!/usr/bin/groovy
@Library('digital-jenkins-library@master')
def digitalCommands = new com.digital.jenkins.DigitalCommands()
def notifications = new com.digital.jenkins.Notifications()

podTemplate(label: 'ratelimit-pod',
        containers: [
                containerTemplate(
                        name: 'glide-golang',
                        image: 'instrumentisto/glide:0.13.1-go1.9',
                        ttyEnabled: true,
                        command: 'cat'
                ),
                containerTemplate(
                        name: 'docker',
                        image: 'docker:stable',
                        ttyEnabled: true,
                        command: 'cat'
                ),
        ],
        volumes: [
                secretVolume(secretName: 'webs-docker-hub', mountPath: '/mnt/webs-docker-hub'),
                hostPathVolume(hostPath: '/var/run/docker.sock', mountPath: '/var/run/docker.sock')
        ]
) {
    node('ratelimit-pod') {
        properties([pipelineTriggers([pollSCM('* */2 * * *')])])
        // checkout used to resolve fatal not a git repo at home dir error
        def varSCM = checkout scm
        def baseDir = sh(script: "pwd", returnStdout: true).trim()
        sh("mkdir -p bin")
        sh("mkdir -p src/github.com/lyft/ratelimit")
        dir("src/github.com/lyft/ratelimit") {
            varSCM = checkout scm
        }
        def IMAGE_TAG = digitalCommands.createDockerImageTag(varSCM)
        try {
            stage('build') {
                container('glide-golang') {
                    dir("src/github.com/lyft/ratelimit") {
                        sh(""" 
                        export GOPATH="${baseDir}"
                        export GOBIN="${baseDir}/bin"                       
                        glide install
                        apk add --no-cache make bash
                        make compile
                     """)
                    }
                }
            }

            stage('Create Docker Image') {
                container('docker') {
                    dir("src/github.com/lyft/ratelimit") {
                        sh("docker build -t webs/ratelimit:${IMAGE_TAG} .")
                    }
                }
            }

            stage('Publish Docker Image') {
                container('docker') {
                    dir("src/github.com/lyft/ratelimit") {
                        sh("cp /mnt/webs-docker-hub/.dockercfg ~/.dockercfg")
                        sh("docker push webs/ratelimit:${IMAGE_TAG}")
                    }
                }
            }
        } catch (e) {
            // If there was an exception thrown, the build failed
            currentBuild.result = "FAILED"
            throw e
        } finally {
            // Success or failure, always send notifications
            notifications(currentBuild.result, 'DigitalJenkinsNotification', varSCM)
        }
    }
}
